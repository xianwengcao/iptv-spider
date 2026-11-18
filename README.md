# iptv-spider

**上海电信 IPTV 频道信息抓取服务**

从上海电信 IPTV 认证接口抓取**频道列表**和**EPG 节目单**，在内存中维护一份实时数据，通过 HTTP 接口对内网提供 **M3U 播放列表**和 **EPG 节目单**，供 xteve、PotPlayer、TiviMate、Kodi 等播放器 / EPG 工具直接订阅。


---

##  特性

- **零外部依赖**：数据全部存内存无需数据库，软件 + config.yaml 即可运行
- **版本号缓存**：所有 API 响应的缓存 key 拼接数据版本号，数据一更新旧缓存立即作废
- **FCC/回放**：直播地址、FCC 加速节点、回放地址随运营商数据自动对齐
- **自定义频道**：`channel_mappings` 可添加节目单里没有的频道
- **频道排序/过滤**：`name_sequence` 自定义播放列表顺序，`exclude_channels` 过滤不需要的频道
- **出站网卡绑定**：多网卡主机可指定软件通信走专网口，支持按网卡名或本机 IP 绑定

---

##  环境要求

1. 上海电信 IPTV 机顶盒账号（必须开通 IPTV 服务，找到相关参数）
2. 程序所在主机**能访问 IPTV 专网**（公网环境无法抓取，回放地址同样需要专网；回放凭证与 IP 绑定，只能本人使用）
3. OpenWrt 用户需补齐时区数据：

```bash
opkg update
opkg install zoneinfo-asia
ln -sf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime
```

---

##  快速开始

### 1. 下载

到 [Releases](https://github.com/xianwengcao/iptv-spider/releases) 页面下载对应平台的二进制：

| 产物名 | 平台 |
| --- | --- |
| `iptv-spider_linux_amd64` | Linux x86-64 |
| `iptv-spider_linux_386` | Linux x86-32 |
| `iptv-spider_linux_arm64` | Linux ARM-64 |
| `iptv-spider_linux_arm` | Linux ARM-32 |
| `iptv-spider_windows_amd64.exe` | Windows x86-64 |
| `iptv-spider_windows_386.exe` | Windows x86-32 |

### 2. 配置

把 `config.yaml` 放到与程序相同目录，按需修改：

```yaml
# ===== 基础 =====
system:
  addr: '0.0.0.0:8888'   # HTTP 监听地址
  dev: 'br-iptv'          # 出站网卡绑定（网卡名或本机 IP，留空走系统路由）

# ===== 机顶盒认证信息 =====
stb:
  uid: 'xxxxxxxxx@etv1'    # IPTV 账号
  mac: 'xx:xx:xx:xx:xx:xx' # 机顶盒 MAC
  sn: '000xxxxxxxxxxxxxxx' # 机顶盒 SN
  ip: '30.xx.xx.xx'        # 专网 IP
  auth_host: '222.68.208.73:7001'

# ===== EPG / 播放列表 =====
epg:
  fetch_cron: '0 0 0,6,12,18 * * *'  # 拉取轮次（6 段 cron，含秒），每轮：认证 → 频道列表 → 节目单
  rtp_url: 'http://192.168.50.254:5140/rtp/'   # rtp2httpd 的 rtp 前缀（默认模式播放地址用它拼装）
  playseek: "&playseek={utc:YmdHMS}-{utcend:YmdHMS}"  # 回放时移参数

...

```

### 3. 运行

```bash
./iptv-spider
```

启动后自动执行第一轮抓取，随后按 `fetch_cron` 定时抓取并刷新内存；每天一次兜底清理过期数据。日志写入 `log/` 目录。

---

##  API 接口

| 接口 | 说明 |
| --- | --- |
| `GET /api/m3u8` | 生成 M3U 播放列表（HTTP-RTP 地址，有 FCC 的频道自动追加加速参数） |
| `GET /api/epg?daysAgo=3` | 生成 XMLTV 节目单，`daysAgo` 控制回溯天数（≤7） |
| `GET /api/run` | 手动触发一轮数据抓取 |
| `GET /api/healthz` | 健康检查，返回数据版本号与频道/节目单规模 |
| `GET /api/schedule` | 查看定时任务的上次/下次执行时间 |

示例：

```text
http://192.168.50.254:8888/api/m3u8
http://192.168.50.254:8888/api/epg?daysAgo=3
```

---

##  频道自定义（channel_mappings）

针对拉取的数据里有播放地址但没有频道名称或其他相关信息、或需要特殊处理的频道：

```yaml
epg:
  channel_mappings:
    - id: '139'                     # 频道 ID（user_channel_id）
      igmp: 'igmp://233.18.204.215:5140'  # 组播地址
      name: "CCTV-16"               # 名称（tvg-name）
      logo: "CCTV-16.png"           # logo 文件名
      group: "央视"                  # 分组
      display_name: "CCTV-16 4K"    # 可选：覆盖播放器显示的频道名
      id_lookup: true               # 可选：见下方说明
```

字段语义：

| 字段 | 语义 |
| --- | --- |
| `igmp` | 直播地址。默认用它去运营商频道数据里查询匹配，FCC / 回放地址随匹配到的记录 |
| `id_lookup: true` | 此频道按 `id` 匹配频道信息：**整条记录按 id 借用**仅直播地址用配置的 `igmp` |
| `display_name` | 覆盖逗号后播放器显示的频道名。设置后与原频道**并存为两条**，不覆盖原频道 |
| `name_sequence` | 频道排序表。按"group-title"后面的"显示名"匹配排序，未上榜的频道排在最后 |

其他配置：

- `exclude_channels`：过滤表，表内频道不输出到m3u中
- `logo_url` / `xml_url`：EPG 输出里的 logo 与节目单引用前缀
- `logo/` 目录：内置常用频道 logo（运行时可选资源，不部署则输出空 logo）

---

## 📄 许可

仅供学习研究，禁止商用。使用本项目产生的一切后果由使用者自行承担。
