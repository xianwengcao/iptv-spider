package config

type Stb struct {
	SN  string `mapstructure:"sn" json:"sn" yaml:"sn"`
	UID string `mapstructure:"uid" json:"uid" yaml:"uid"`
	MAC string `mapstructure:"mac" json:"mac" yaml:"mac"`
	IP  string `mapstructure:"ip" json:"ip" yaml:"ip"`

	// Type 机顶盒型号，portal 认证时作为 stbtype 参数提交；留空使用内置默认值 B860A。
	Type string `mapstructure:"type" json:"type" yaml:"type"`

	// AuthHost 4K 认证服务器地址（host / host:port / http://host 均可）。
	// 认证流程与 -netcheck 诊断共用此项，服务器地址变更时改配置即可；
	// 留空使用内置默认值 222.68.208.73:7001。
	AuthHost string `mapstructure:"auth_host" json:"auth_host" yaml:"auth_host"`
}
