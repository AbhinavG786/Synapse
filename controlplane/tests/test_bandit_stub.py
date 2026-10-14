"""
test_bandit_stub.py — Phase 0 control plane smoke tests.
Full bandit property tests (hypothesis) in Phase 5.1.
"""
from __future__ import annotations

import pytest
from synapse_cp.bandit.bandit import LinUCB


def test_linucb_select_single_arm() -> None:
    bandit = LinUCB(d=16, alpha=1.0)
    result = bandit.select(["node-1"], {"node-1": [0.0] * 16})
    assert result == "node-1"


def test_linucb_select_empty_raises() -> None:
    bandit = LinUCB(d=16)
    with pytest.raises(ValueError):
        bandit.select([], {})


def test_linucb_snapshot_shape() -> None:
    bandit = LinUCB(d=16)
    snap = bandit.snapshot()
    assert snap["d"] == 16
    assert snap["algo"] == "linucb"
    assert "arms" in snap
