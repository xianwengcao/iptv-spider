package config

type Zap struct {
	Level        string `mapstructure:"level" json:"level" yaml:"level"`                   // 级别
	Prefix       string `mapstructure:"prefix" json:"prefix" yaml:"prefix"`                // 日志前缀
	Director     string `mapstructure:"director" json:"director"  yaml:"director"`         // 日志文件夹
	EncodeLevel  string `mapstructure:"encode-level" json:"encodeLevel" yaml:"encode-level"` // 编码级
	LogInConsole bool   `mapstructure:"log-in-console" json:"logInConsole" yaml:"log-in-console"` // 输出控制台
}
