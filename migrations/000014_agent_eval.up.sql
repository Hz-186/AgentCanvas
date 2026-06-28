CREATE TABLE IF NOT EXISTS agent_eval_datasets (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    agent_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    status INT NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at DATETIME NULL,
    INDEX idx_owner_agent (owner_id, agent_id),
    INDEX idx_owner_status (owner_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS agent_eval_cases (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    dataset_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    input_json JSON NOT NULL,
    expected_json JSON NULL,
    tags_json JSON NULL,
    required_tools_json JSON NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at DATETIME NULL,
    INDEX idx_owner_dataset (owner_id, dataset_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS agent_eval_runs (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    agent_id BIGINT NOT NULL,
    dataset_id BIGINT NOT NULL,
    flow_version_id BIGINT NOT NULL DEFAULT 0,
    status VARCHAR(32) NOT NULL,
    total_cases INT NOT NULL DEFAULT 0,
    passed_cases INT NOT NULL DEFAULT 0,
    failed_cases INT NOT NULL DEFAULT 0,
    success_rate DOUBLE NOT NULL DEFAULT 0,
    summary_json JSON NULL,
    error_message TEXT,
    started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at DATETIME NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_owner_dataset (owner_id, dataset_id),
    INDEX idx_owner_agent (owner_id, agent_id),
    INDEX idx_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS agent_eval_results (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    eval_run_id BIGINT NOT NULL,
    eval_case_id BIGINT NOT NULL,
    agent_run_id BIGINT NULL,
    status VARCHAR(32) NOT NULL,
    score DOUBLE NOT NULL DEFAULT 0,
    reason TEXT,
    output_json JSON NULL,
    metrics_json JSON NULL,
    error_message TEXT,
    latency_ms INT NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_owner_eval_run (owner_id, eval_run_id),
    INDEX idx_eval_case (eval_case_id),
    INDEX idx_agent_run (agent_run_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
