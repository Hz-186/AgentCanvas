CREATE TABLE IF NOT EXISTS mcp_servers (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    transport VARCHAR(32) NOT NULL DEFAULT 'sse',
    endpoint_url TEXT,
    command TEXT,
    args_json JSON,
    env_json JSON,
    status TINYINT NOT NULL DEFAULT 1,
    last_error TEXT,
    discovered_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME NULL,
    INDEX idx_mcp_servers_owner (owner_id, deleted_at),
    INDEX idx_mcp_servers_status (owner_id, status)
);

CREATE TABLE IF NOT EXISTS mcp_tool_cache (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    server_id BIGINT NOT NULL,
    tool_name VARCHAR(128) NOT NULL,
    description TEXT,
    parameters_json JSON,
    schema_hash VARCHAR(128),
    cached_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE KEY uniq_mcp_tool_cache_server_name (owner_id, server_id, tool_name),
    INDEX idx_mcp_tool_cache_server (owner_id, server_id)
);
