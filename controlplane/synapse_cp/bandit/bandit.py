"""
bandit.py — LinUCB bandit stub.

Full implementation in Phase 5.1.
Reference: Li, Chu, Langford, Schapire, WWW 2010.
"""
from __future__ import annotations


class LinUCB:
    """Stub for the disjoint LinUCB bandit (Phase 5.1).

    Each arm has its own A_a (d×d) and b_a (d,) matrices.
    Sherman-Morrison updates are used to maintain A_a^{-1} directly.
    """

    def __init__(self, d: int, alpha: float = 1.0, lambda_reg: float = 1.0) -> None:
        self.d = d
        self.alpha = alpha
        self.lambda_reg = lambda_reg
        # arms: dict[node_id -> ArmState] — populated in Phase 5.1
        self._arms: dict[str, object] = {}

    def select(self, node_ids: list[str], context_per_arm: dict[str, list[float]]) -> str:
        """Select the arm with the highest UCB score. Stub: returns first node."""
        if not node_ids:
            raise ValueError("no arms to select from")
        return node_ids[0]

    def update(self, node_id: str, context: list[float], reward: float) -> None:
        """Update arm parameters given observed reward. Stub: no-op."""
        pass

    def snapshot(self) -> dict:
        """Return a serialisable policy snapshot dict. Stub."""
        return {
            "version": 0,
            "algo": "linucb",
            "d": self.d,
            "alpha": self.alpha,
            "arms": [],
        }
