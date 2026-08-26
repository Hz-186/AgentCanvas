ALTER TABLE `agent_improvement_reviews` DROP COLUMN `agent_release_id`;
ALTER TABLE `agent_turns` DROP COLUMN `agent_release_id`;
ALTER TABLE `agents` DROP COLUMN `current_release_id`;
ALTER TABLE `agent_runs` DROP COLUMN `agent_release_id`;
ALTER TABLE `conversations` DROP COLUMN `agent_release_id`;

DROP TABLE `agent_releases`;
