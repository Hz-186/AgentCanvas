UPDATE `conversations`
SET `agent_mode` = 'react'
WHERE `agent_mode` = 'default';

ALTER TABLE `conversations`
  MODIFY COLUMN `agent_mode` varchar(32) NOT NULL DEFAULT 'react';
DROP TABLE IF EXISTS `agent_thread_goal_deferrals`;
DROP TABLE IF EXISTS `agent_thread_goals`;
