UPDATE `conversations`
SET `agent_mode` = CASE
  WHEN `agent_mode` IN ('goal', 'react', '') THEN 'default'
  WHEN `agent_mode` = 'plan_execute' THEN 'plan'
  ELSE `agent_mode`
END;

ALTER TABLE `conversations`
  MODIFY COLUMN `agent_mode` varchar(32) NOT NULL DEFAULT 'default';

CREATE TABLE `agent_thread_goals` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `conversation_id` bigint NOT NULL,
  `goal_id` varchar(64) NOT NULL,
  `objective` text NOT NULL,
  `status` varchar(32) NOT NULL DEFAULT 'active',
  `token_budget` bigint DEFAULT NULL,
  `tokens_used` bigint NOT NULL DEFAULT 0,
  `time_used_seconds` bigint NOT NULL DEFAULT 0,
  `created_at` datetime(6) NOT NULL,
  `updated_at` datetime(6) NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `ux_agent_thread_goals_conversation` (`owner_id`,`conversation_id`),
  UNIQUE KEY `ux_agent_thread_goals_goal_id` (`goal_id`),
  KEY `idx_agent_thread_goals_status` (`owner_id`,`status`),
  CONSTRAINT `fk_agent_thread_goals_conversation` FOREIGN KEY (`conversation_id`) REFERENCES `conversations` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE `agent_thread_goal_deferrals` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `conversation_id` bigint NOT NULL,
  `created_at` datetime(6) NOT NULL,
  `updated_at` datetime(6) NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `ux_goal_deferral` (`owner_id`,`conversation_id`),
  CONSTRAINT `fk_agent_goal_deferrals_conversation` FOREIGN KEY (`conversation_id`) REFERENCES `conversations` (`id`) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
