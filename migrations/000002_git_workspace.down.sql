ALTER TABLE `agent_runs` DROP INDEX `idx_agent_runs_workspace`, DROP COLUMN `workspace_id`;
ALTER TABLE `conversations` DROP INDEX `idx_conversations_project`, DROP COLUMN `workspace_mode`, DROP COLUMN `project_id`;
DROP TABLE IF EXISTS `agent_workspaces`;
DROP TABLE IF EXISTS `project_folders`;
DROP TABLE IF EXISTS `projects`;
