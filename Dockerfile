FROM python:3.11-slim

WORKDIR /app

RUN pip install --no-cache-dir numpy pandas jupyter nbconvert

COPY . .

CMD ["python", "scripts/run_analysis.py"]
