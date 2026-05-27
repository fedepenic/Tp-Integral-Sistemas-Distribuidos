package joiners

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/aggregators"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/middleware"
	"github.com/fedepenic/Tp-Integral-Sistemas-Distribuidos/system/internal/protocol"
)

const (
	accountsDataType   = "accounts"
	maxPerBankDataType = "max_per_bank"
	defaultClientID    = "default"
)

type JoinQ2 struct {
	inputMW            middleware.Middleware
	outputMW           middleware.Middleware
	controlPub         middleware.Middleware
	controlSub         middleware.Middleware
	accountsUpstream   int
	maxPerBankUpstream int

	mu            sync.Mutex
	cond          *sync.Cond
	globalPending int
	clientPending map[string]int

	eofAccountsCount map[string]int
	eofMaxCount      map[string]int
	propagated       map[string]bool

	accountsByClient   map[string]map[string]protocol.Account
	pendingMaxByClient map[string]map[string][]aggregators.MaxPerBankResult
}

func NewJoinQ2(
	inputMW, outputMW, controlPub, controlSub middleware.Middleware,
	accountsUpstream, maxPerBankUpstream int,
) *JoinQ2 {
	j := &JoinQ2{
		inputMW:            inputMW,
		outputMW:           outputMW,
		controlPub:         controlPub,
		controlSub:         controlSub,
		accountsUpstream:   accountsUpstream,
		maxPerBankUpstream: maxPerBankUpstream,
		clientPending:      make(map[string]int),
		eofAccountsCount:   make(map[string]int),
		eofMaxCount:        make(map[string]int),
		propagated:         make(map[string]bool),
		accountsByClient:   make(map[string]map[string]protocol.Account),
		pendingMaxByClient: make(map[string]map[string][]aggregators.MaxPerBankResult),
	}
	j.cond = sync.NewCond(&j.mu)
	return j
}

func (j *JoinQ2) Run() {
	go func() {
		err := j.controlSub.StartConsuming(j.handleEOFBroadcast)
		if err != nil && err != middleware.ErrMessageMiddlewareDisconnected {
			log.Printf("[join_q2] control consumer error: %v", err)
		}
	}()

	err := j.inputMW.StartConsuming(j.handleData)
	if err != nil && err != middleware.ErrMessageMiddlewareDisconnected {
		log.Printf("[join_q2] input consumer error: %v", err)
	}
}

// handleData procesa data y cuando llega un EOF lo retransmite via controlPub
// — igual que AggregatorWorker.handleDataMessage
func (j *JoinQ2) handleData(msg middleware.Message, ack func(), nack func()) {
	j.mu.Lock()
	j.globalPending++
	j.mu.Unlock()

	var batch protocol.Batch
	if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
		log.Printf("[join_q2] malformed batch: %v", err)
		j.mu.Lock()
		j.globalPending--
		j.cond.Broadcast()
		j.mu.Unlock()
		ack()
		return
	}

	clientID := batch.ClientID
	if clientID == "" {
		clientID = defaultClientID
	}

	if batch.Type == protocol.BatchTypeEOF {
		j.mu.Lock()
		j.globalPending--
		j.cond.Broadcast()
		j.mu.Unlock()
		if err := j.controlPub.Send(msg); err != nil {
			log.Printf("[join_q2] broadcast EOF error: %v", err)
			nack()
			return
		}
		ack()
		return
	}

	j.mu.Lock()
	j.globalPending--
	j.clientPending[clientID]++
	j.mu.Unlock()

	var err error
	switch {
	case batch.DataType == accountsDataType || batch.Type == protocol.BatchTypeAccounts:
		err = j.processAccounts(clientID, batch.Accounts)
	case batch.DataType == maxPerBankDataType:
		err = j.processMaxResults(clientID, batch.Records)
	}

	j.mu.Lock()
	j.clientPending[clientID]--
	j.cond.Broadcast()
	j.mu.Unlock()

	if err != nil {
		nack()
		return
	}
	ack()
}

// handleEOFBroadcast — igual que AggregatorWorker.handleEOFBroadcast
// cuenta EOFs por fuente y por cliente, drena in-flight y propaga downstream
func (j *JoinQ2) handleEOFBroadcast(msg middleware.Message, ack func(), nack func()) {
	var batch protocol.Batch
	if err := json.Unmarshal([]byte(msg.Body), &batch); err != nil {
		log.Printf("[join_q2] malformed EOF broadcast: %v", err)
		ack()
		return
	}
	if batch.Type != protocol.BatchTypeEOF {
		ack()
		return
	}

	clientID := batch.ClientID
	if clientID == "" {
		clientID = defaultClientID
	}

	j.mu.Lock()
	switch batch.DataType {
	case accountsDataType:
		j.eofAccountsCount[clientID]++
	case maxPerBankDataType:
		j.eofMaxCount[clientID]++
	default:
		j.eofMaxCount[clientID]++
	}
	accountsDone := j.eofAccountsCount[clientID] >= j.accountsUpstream
	maxDone := j.eofMaxCount[clientID] >= j.maxPerBankUpstream
	alreadyPropagated := j.propagated[clientID]
	j.mu.Unlock()

	log.Printf("[join_q2] EOF broadcast client=%s dataType=%s accounts=%d/%d max=%d/%d",
		clientID, batch.DataType,
		j.eofAccountsCount[clientID], j.accountsUpstream,
		j.eofMaxCount[clientID], j.maxPerBankUpstream,
	)
	ack()

	if !accountsDone || !maxDone || alreadyPropagated {
		return
	}

	j.mu.Lock()
	if j.propagated[clientID] {
		j.mu.Unlock()
		return
	}
	j.propagated[clientID] = true

	// drenar mensajes en vuelo antes de propagar
	for j.globalPending > 0 || j.clientPending[clientID] > 0 {
		j.cond.Wait()
	}
	j.mu.Unlock()

	log.Printf("[join_q2] all EOFs received for client=%s — flushing pending and propagating", clientID)

	if err := j.flushPending(clientID); err != nil {
		log.Printf("[join_q2] error flushing pending for client=%s: %v", clientID, err)
	}

	outBatch := protocol.Batch{
		Type:     protocol.BatchTypeEOF,
		ClientID: clientID,
		DataType: maxPerBankDataType,
	}
	data, err := json.Marshal(outBatch)
	if err != nil {
		log.Printf("[join_q2] marshal EOF: %v", err)
		return
	}
	if err := j.outputMW.Send(middleware.Message{Body: string(data)}); err != nil {
		log.Printf("[join_q2] send EOF downstream: %v", err)
		return
	}

	j.mu.Lock()
	delete(j.accountsByClient, clientID)
	delete(j.pendingMaxByClient, clientID)
	delete(j.eofAccountsCount, clientID)
	delete(j.eofMaxCount, clientID)
	delete(j.propagated, clientID)
	j.mu.Unlock()
}

// flushPending emite los resultados max_per_bank que quedaron sin account
// al momento de llegada — al llegar acá ambas fuentes ya cerraron,
// así que lo que quede en pending nunca va a tener match: se descarta.
// Si querés emitirlos igual sin BankName, cambiá este método.
func (j *JoinQ2) flushPending(clientID string) error {
	j.mu.Lock()
	pending := j.pendingMaxByClient[clientID]
	j.mu.Unlock()

	if len(pending) == 0 {
		return nil
	}

	var unmatched []aggregators.MaxPerBankResult
	for _, results := range pending {
		unmatched = append(unmatched, results...)
	}
	log.Printf("[join_q2] client=%s: %d unmatched max_per_bank results at EOF (no account found)", clientID, len(unmatched))
	// descartamos — si querés emitirlos igual, llamá j.emitResults(clientID, unmatched)
	return nil
}

func (j *JoinQ2) processAccounts(clientID string, accounts []protocol.Account) error {
	if len(accounts) == 0 {
		return nil
	}

	var toEmit []aggregators.MaxPerBankResult

	j.mu.Lock()
	accountMap := j.accountsByClient[clientID]
	if accountMap == nil {
		accountMap = make(map[string]protocol.Account)
		j.accountsByClient[clientID] = accountMap
	}
	pending := j.pendingMaxByClient[clientID]
	if pending == nil {
		pending = make(map[string][]aggregators.MaxPerBankResult)
		j.pendingMaxByClient[clientID] = pending
	}
	for _, account := range accounts {
		accountMap[account.BankID] = account
		if queued := pending[account.BankID]; len(queued) > 0 {
			for _, res := range queued {
				toEmit = append(toEmit, aggregators.MaxPerBankResult{
					BankID:        res.BankID,
					BankName:      account.BankName,
					SourceAccount: res.SourceAccount,
					MaxAmountUSD:  res.MaxAmountUSD,
				})
			}
			delete(pending, account.BankID)
		}
	}
	j.mu.Unlock()

	return j.emitResults(clientID, toEmit)
}

func (j *JoinQ2) processMaxResults(clientID string, records json.RawMessage) error {
	var results []aggregators.MaxPerBankResult
	if err := json.Unmarshal(records, &results); err != nil {
		return err
	}

	var toEmit []aggregators.MaxPerBankResult

	j.mu.Lock()
	accountMap := j.accountsByClient[clientID]
	pending := j.pendingMaxByClient[clientID]
	if pending == nil {
		pending = make(map[string][]aggregators.MaxPerBankResult)
		j.pendingMaxByClient[clientID] = pending
	}
	for _, res := range results {
		if account, ok := accountMap[res.BankID]; ok {
			toEmit = append(toEmit, aggregators.MaxPerBankResult{
				BankID:        res.BankID,
				BankName:      account.BankName,
				SourceAccount: res.SourceAccount,
				MaxAmountUSD:  res.MaxAmountUSD,
			})
		} else {
			pending[res.BankID] = append(pending[res.BankID], res)
		}
	}
	j.mu.Unlock()

	return j.emitResults(clientID, toEmit)
}

func (j *JoinQ2) emitResults(clientID string, results []aggregators.MaxPerBankResult) error {
	if len(results) == 0 {
		return nil
	}
	records, err := json.Marshal(results)
	if err != nil {
		return err
	}
	outBatch := protocol.Batch{
		Type:     protocol.BatchTypeData,
		ClientID: clientID,
		DataType: maxPerBankDataType,
		Records:  records,
	}
	data, err := json.Marshal(outBatch)
	if err != nil {
		return err
	}
	return j.outputMW.Send(middleware.Message{Body: string(data)})
}

func (j *JoinQ2) Close() {
	_ = j.inputMW.Close()
	_ = j.outputMW.Close()
	_ = j.controlPub.Close()
	_ = j.controlSub.Close()
}
