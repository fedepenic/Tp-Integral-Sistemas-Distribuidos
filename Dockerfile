FROM python:3.11-slim

WORKDIR /app

RUN pip install --no-cache-dir numpy pandas jupyter nbconvert

COPY . .

CMD ["jupyter", "nbconvert", "--to", "notebook", "--execute", \
     "--ExecutePreprocessor.timeout=600", \
     "money-laundering-analysis.ipynb"]
