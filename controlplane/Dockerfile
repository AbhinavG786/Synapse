# Synapse Control Plane Dockerfile
# Python 3.11, slim base
FROM python:3.11-slim

WORKDIR /app

# Install dependencies first (layer cache)
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy source
COPY . .

EXPOSE 9080 9090

CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "9080", "--workers", "1"]
