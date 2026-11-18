package config

type Epg struct {
	Generator string `mapstructure:"generator" json:"generator" yaml:"generator"`
	Source    string `mapstructure:"source" json:"source" yaml:"source"`
	XmlUrl    string `mapstructure:"xml_url" json:"xml_url" yaml:"xml_url"`
	RtspUrl   string `mapstructure:"rtsp_url" json:"rtsp_url" yaml:"rtsp_url"`
	RtpUrl    string `mapstructure:"rtp_url" json:"rtp_url" yaml:"rtp_url"`
	LogoUrl   string `mapstructure:"logo_url" json:"logo_url" yaml:"logo_url"`
	// FetchCron 拉取轮次计划（cron 表达式），如 '0 0 0,6,12,18 * * *'（每天 0/6/12/18 点）。
	// 每轮依次执行「认证 → 频道信息列表 → 节目单」，全量拉取所有频道；
	// 会话不跨轮保活，因此本表达式的间隔同时决定播放地址与运营商的对齐频率。
	FetchCron       string           `mapstructure:"fetch_cron" json:"fetch_cron" yaml:"fetch_cron"`
	Playseek        string           `mapstructure:"playseek" json:"playseek" yaml:"playseek"`
	ChannelMappings []ChannelMapping `mapstructure:"channel_mappings" yaml:"channel_mappings"`
	NameSequence    []ChannelMapping `mapstructure:"name_sequence" yaml:"name_sequence"`
	ExcludeChannels []string         `mapstructure:"exclude_channels" yaml:"exclude_channels"` // 过滤掉的频道 CommName 列表
}

type ChannelMapping struct {
	Id          string `mapstructure:"id" json:"id" yaml:"id"`
	Igmp        string `mapstructure:"Igmp" json:"igmp" yaml:"igmp"`
	Name        string `mapstructure:"name" json:"name" yaml:"name"`
	Logo        string `mapstructure:"logo" json:"logo" yaml:"logo"`
	Group       string `mapstructure:"group" json:"group" yaml:"group"`
	IdLookup    bool   `mapstructure:"id_lookup" json:"id_lookup" yaml:"id_lookup"`          // true 时整条频道记录（catchup-source、FCC）按 id（user_channel_id）借用，仅直播地址用配置的 igmp
	DisplayName string `mapstructure:"display_name" json:"display_name" yaml:"display_name"` // 覆盖逗号后播放器显示的频道名称
}
