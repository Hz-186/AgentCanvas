# Design

纯删除型重构，无新架构：先挪三个消息仓储接口入 pipeline 文件，再按「包内 → 相邻层 → 验证」三步删除；所有删除项均已证实零生产调用者（见 exploration-handoff 决策清单 D3–D7）。
