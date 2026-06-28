CREATE TABLE IF NOT EXISTS workflow_teams (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    supervisor_workflow_id BIGINT NOT NULL,
    handoff_strategy VARCHAR(32) NOT NULL DEFAULT 'supervisor',
    max_depth INT NOT NULL DEFAULT 5,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_owner_team_name (owner_id, name),
    INDEX idx_owner_id (owner_id),
    INDEX idx_supervisor (supervisor_workflow_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS workflow_team_members (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    team_id BIGINT NOT NULL,
    workflow_id BIGINT NOT NULL,
    role VARCHAR(64) NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_team_workflow (team_id, workflow_id),
    INDEX idx_owner_team (owner_id, team_id),
    INDEX idx_workflow_id (workflow_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
