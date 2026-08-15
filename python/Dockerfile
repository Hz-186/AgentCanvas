FROM python:3.12-slim

WORKDIR /app/python

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

COPY python/requirements.txt ./requirements.txt
RUN pip install --no-cache-dir --disable-pip-version-check -r requirements.txt \
    && useradd --create-home --uid 10001 agentcanvas

COPY --chown=agentcanvas:agentcanvas python/ /app/python/

USER agentcanvas

EXPOSE 50051

CMD ["python", "-m", "agentcanvas_bridge"]
