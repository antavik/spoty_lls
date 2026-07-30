FROM python:3.13-slim AS base

ENV PYTHONUNBUFFERED=1

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY src/ ./

FROM base AS test

ENV PYTHONPATH=/app

COPY requirements-dev.txt .
RUN pip install --no-cache-dir -r requirements-dev.txt

COPY pyproject.toml .
COPY tests/ ./tests/

FROM base AS prod

RUN useradd -m app
USER app

ENTRYPOINT ["python", "main.py"]
