package conf

import "embed"

// ProviderFiles 内嵌所有预置模型供应商配置 (conf/providers/*.yaml)。
// 这样配置随二进制一起分发,运行时无需依赖外部文件路径。
//
//go:embed providers/*.yaml
var ProviderFiles embed.FS
