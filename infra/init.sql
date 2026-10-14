-- Synapse TimescaleDB / PostgreSQL initialisation
-- Applied automatically on first `docker compose up` via docker-entrypoint-initdb.d
-- Phase 0: schema creation only. Continuous aggregates added in Phase 3.

-- ============================================================
-- Extensions
-- ============================================================
CREATE EXTENSION IF NOT EXISTS timescaledb;
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- ============================================================
-- Config schema (PostgreSQL / relational)
-- ============================================================
CREATE SCHEMA IF NOT EXISTS config;

-- Backend node registry
CREATE TABLE IF NOT EXISTS config.nodes (
    node_id         TEXT        PRIMARY KEY,
    address         TEXT        NOT NULL,
    weight          DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    speed_factor    DOUBLE PRECISION NOT NULL DEFAULT 1.0,
    enabled         BOOLEAN     NOT NULL DEFAULT TRUE,
    registered_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Route-class table
CREATE TABLE IF NOT EXISTS config.route_classes (
    id              SERIAL      PRIMARY KEY,
    prefix          TEXT        NOT NULL UNIQUE,
    class_name      TEXT        NOT NULL,  -- 'health','light_crud','medium_query','heavy_compute','io_bound'
    baseline_median_ms DOUBLE PRECISION,    -- used as reward scale
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Policy / experiment configuration
CREATE TABLE IF NOT EXISTS config.policies (
    policy_id       UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    algo            TEXT        NOT NULL,   -- matches proto Algorithm name
    alpha           DOUBLE PRECISION,
    epsilon         DOUBLE PRECISION,
    lambda_reg      DOUBLE PRECISION,
    forgetting_gamma DOUBLE PRECISION,
    sync_interval_ms INTEGER,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    notes           TEXT
);

-- Experiment / run registry
CREATE TABLE IF NOT EXISTS config.runs (
    run_id          TEXT        PRIMARY KEY,
    policy_id       UUID        REFERENCES config.policies(policy_id),
    workload_profile TEXT,
    fault_scenario  TEXT,
    seed            INTEGER,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at        TIMESTAMPTZ,
    status          TEXT        NOT NULL DEFAULT 'running', -- running/completed/failed
    notes           TEXT
);

-- ============================================================
-- Events schema (TimescaleDB hypertable)
-- ============================================================
CREATE SCHEMA IF NOT EXISTS events;

-- Main request-event hypertable (Section 3 storage spec)
CREATE TABLE IF NOT EXISTS events.request_events (
    ts                  TIMESTAMPTZ     NOT NULL,
    req_id              TEXT            NOT NULL,
    run_id              TEXT,
    endpoint            TEXT,
    method              TEXT,
    payload_bytes       BIGINT,
    node_id             TEXT,
    algo                TEXT,
    policy_version      BIGINT,
    ctx                 JSONB,          -- full context vector for OPE
    t_exec_ms           DOUBLE PRECISION,
    status_code         INTEGER,
    timed_out           BOOLEAN,
    error               BOOLEAN,
    reward              DOUBLE PRECISION,
    propensity          DOUBLE PRECISION,
    PRIMARY KEY (ts, req_id)
);

-- Convert to TimescaleDB hypertable, partitioned by time
SELECT create_hypertable(
    'events.request_events',
    'ts',
    chunk_time_interval => INTERVAL '1 day',
    if_not_exists => TRUE
);

-- ============================================================
-- Seed default route classes
-- ============================================================
INSERT INTO config.route_classes (prefix, class_name, baseline_median_ms) VALUES
    ('/health',   'health',        0.2),
    ('/crud/',    'light_crud',    5.0),
    ('/query',    'medium_query',  40.0),
    ('/compute',  'heavy_compute', 300.0),
    ('/io',       'io_bound',      150.0)
ON CONFLICT (prefix) DO NOTHING;

-- ============================================================
-- Seed default backend nodes (overridden by dynamic config in Phase 2)
-- ============================================================
INSERT INTO config.nodes (node_id, address, weight, speed_factor) VALUES
    ('backend-1', 'http://backend-1:9000', 1.0, 1.0),
    ('backend-2', 'http://backend-2:9000', 1.0, 1.0),
    ('backend-3', 'http://backend-3:9000', 1.0, 0.6),
    ('backend-4', 'http://backend-4:9000', 1.0, 1.5)
ON CONFLICT (node_id) DO NOTHING;
