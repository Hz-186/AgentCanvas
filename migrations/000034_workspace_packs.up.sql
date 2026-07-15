CREATE TABLE IF NOT EXISTS workspaces (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    root_path VARCHAR(2048) NOT NULL,
    default_branch VARCHAR(255) NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at DATETIME NULL,
    UNIQUE INDEX uk_workspace_owner_root (owner_id, root_path(512)),
    INDEX idx_workspaces_owner (owner_id, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS workspace_packs (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    workspace_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    allowed_paths_json JSON NOT NULL,
    command_allowlist_json JSON NOT NULL,
    network_enabled TINYINT(1) NOT NULL DEFAULT 0,
    allowed_domains_json JSON NOT NULL,
    docker_image VARCHAR(512) NOT NULL,
    timeout_seconds INT NOT NULL DEFAULT 120,
    cpu_limit VARCHAR(32) NOT NULL DEFAULT '2',
    memory_limit_mb INT NOT NULL DEFAULT 2048,
    process_limit INT NOT NULL DEFAULT 128,
    max_output_bytes INT NOT NULL DEFAULT 1048576,
    checksum CHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at DATETIME NULL,
    INDEX idx_workspace_packs_owner_workspace (owner_id, workspace_id, status),
    INDEX idx_workspace_packs_checksum (checksum)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS workspace_run_leases (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    workspace_id BIGINT NOT NULL,
    run_id BIGINT NOT NULL,
    worktree_path VARCHAR(2048) NOT NULL DEFAULT '',
    lease_token VARCHAR(64) NOT NULL,
    lease_expires_at DATETIME NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE INDEX uk_workspace_active_run (workspace_id, run_id),
    INDEX idx_workspace_lease (workspace_id, status, lease_expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
