"""
main.py — Synapse Control Plane entry point.

Phase 0: stub FastAPI app with health check only.
Full implementation in Phase 5.7.
"""
from __future__ import annotations

from fastapi import FastAPI

app = FastAPI(
    title="Synapse Control Plane",
    description=(
        "Out-of-band ML control plane for Synapse: "
        "LinUCB bandit training, policy snapshot publishing, "
        "telemetry ingestion, and portal API."
    ),
    version="0.0.1",
)


@app.get("/healthz", tags=["meta"])
async def healthz() -> dict:
    """Health check endpoint."""
    return {"status": "ok", "phase": "0-stub"}


@app.get("/policy", tags=["policy"])
async def get_policy() -> dict:
    """Return the current policy snapshot. Stub."""
    return {"status": "stub", "phase": "0", "message": "Phase 5 implementation pending"}


if __name__ == "__main__":
    import uvicorn

    uvicorn.run("main:app", host="0.0.0.0", port=9080, reload=False)
