# FiberHome LG6121F SMS Forwarder

在烽火 LG6121F 5G CPE 本机运行的短信转发器：接收新短信后，通过飞书群自定义机器人推送到飞书。

本方案**不依赖 CPE Web 管理 API、管理后台密码或额外常开主机**。程序直接监听 CPE 内部的 `cpe_new_sms_notify` ubus 事件，并读取 `/data/fhconf/sms` 中的短信文件，再通过飞书云服务推送消息。

> 当前验证环境：FiberHome LG6121F、COMMON/INTL 固件、ARM64 OpenWrt 19.07。其他型号和固件版本需要重新验证。
>
> 已完成实机验证：真实新短信能够推送到飞书。运行时崩溃自动拉起也已验证；**重启后的无人值守自动启动尚未实现**，详见后文。

## 功能

- ubus 新短信事件即时唤醒。
- 每分钟扫描一次短信目录，启动时补扫，避免事件丢失造成漏发。
- 直接读取原厂短信文件，不调用 Web API，不与 Web 后台登录互相挤下线。
- 支持 UTF-16BE 十六进制短信正文，包括中文和 emoji。
- 飞书自定义机器人文本消息、可选签名校验、自定义 CA、超时和错误处理。
- 首次运行建立历史基线，避免把全部历史短信推送到群里。
- 文件名与内容哈希去重，状态持久化到 `/data`。
- 飞书失败不记录成功，自动重试。
- 等待新文件写入稳定后再读取。
- 日志自动轮转，单个文件约 1 MiB，保留一个备份。
- procd 运行时管理和进程崩溃自动拉起。
- 不修改短信已读状态，不删除短信，不发送 AT 指令，不修改 rootfs。

## 工作原理

```text
CPE 收到短信
    │
    ▼
mobilenetwork 写入 /data/fhconf/sms/<短信文件>
    │
    ├─ 发布 cpe_new_sms_notify（只包含短信索引，不包含正文）
    │
    ▼
SMS Forwarder 被 ubus 事件唤醒
    │
    ├─ 启动和每分钟定时扫描负责补漏
    │
    ▼
等待文件写入稳定
    │
    ├─ 读取短信文件，解码 UTF-16BE 十六进制正文
    ├─ 从 value.conf 或文件名取得发件人和时间
    │
    ▼
调用飞书自定义机器人 Webhook
    │
    ├─ 成功：将文件名和内容哈希写入 state.json
    └─ 失败：不记录成功，15 秒后重试
```

程序不会把 ubus 当作可靠的消息队列。事件只负责降低延迟，文件扫描和持久化状态才负责补漏与去重。

## 准备条件

电脑需要：

- 与 CPE 处于同一局域网，能够访问 `192.168.8.1`。
- Go 1.25 或兼容版本，用于交叉编译 ARM64 程序。
- Python 3，仅用于安装期间通过 HTTP 向 CPE 传输二进制。
- Telnet 客户端。

CPE 需要：

- FiberHome LG6121F。
- 已插入可接收短信的 SIM 卡，并能正常联网。
- 可以访问飞书 `https://open.feishu.cn`。
- 本文所验证的 Telnet 登录方式与你的固件兼容。

CPE 上不需要安装 Go、Python、Node.js 或其他依赖。

## 创建飞书机器人

1. 在飞书群中打开群设置，进入“群机器人”。
2. 添加“自定义机器人”。
3. 复制 Webhook 地址，格式类似：

   ```text
   https://open.feishu.cn/open-apis/bot/v2/hook/<你的-token>
   ```

4. 建议开启“签名校验”，复制签名密钥。
5. 如果启用了自定义关键词，确保关键词能够匹配程序消息前缀，例如 `CPE SMS`。

Webhook 和签名密钥都属于敏感信息，不要提交到 Git 仓库或写入公开文档。

## 编译

在源码目录执行：

```sh
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -ldflags="-s -w" -o dist/sms-forwarder-arm64 .
```

得到静态 ARM64 二进制：

```text
dist/sms-forwarder-arm64
```

在电脑上用 HTTP 临时共享这个文件：

```sh
cd dist
python3 -m http.server 18080 --bind 0.0.0.0
```

保持这个终端运行，安装结束后按 `Ctrl+C` 关闭。后文用 `<电脑IP>` 表示电脑在 CPE 局域网中的地址，例如 `192.168.8.102`。

## 获取 root shell（Telnet）

这里只使用已经实机验证过的 Telnet 方案，不涉及 SSH、ADB、命令注入或刷机。

### 1. 取得 bridge MAC 和计算 key

Telnet 开启 key 和登录密码都基于 CPE 的 **bridge MAC**。

一种查询方式是打开：

```text
http://192.168.8.1/login.html
```

等页面加载完成后，在浏览器开发者工具控制台执行：

```js
$post('get_device_info', null, 'nocheck').then(d => console.log(d.model_name, d.brmac))
```

记录输出中的 `brmac`。

以下面这个**示例 MAC** 演示计算，实际使用时必须替换成自己的值：

```text
28:F7:D6:12:34:56
```

计算规则：

- 去掉冒号，取最后六位十六进制字符，全部大写：`123456`。
- Telnet 开启 key：将这个十六进制数加一，得到 `123457`。
- Telnet `admin` 密码：`hg2x0` 加上未加一的 MAC 后六位，即 `hg2x0123456`。
- `su root` 密码：`f1ber@dm!n` 加上未加一的 MAC 后六位，即 `f1ber@dm!n123456`。

注意：加一是十六进制加法，可能需要进位，不只是简单修改最后一个字符。

### 2. 开启 Telnet

把 `<KEY>` 换成计算出的 key：

```sh
curl 'http://192.168.8.1/cgi-bin/fh_telnet?enable=1&key=<KEY>'
```

使用上面的示例时，完整命令是：

```sh
curl 'http://192.168.8.1/cgi-bin/fh_telnet?enable=1&key=123457'
```

接口会返回 `telnet开启` 一类的状态文本。检查端口：

```sh
nc -vz 192.168.8.1 23
```

### 3. 登录并切换 root

```sh
telnet 192.168.8.1
```

按提示输入：

```text
login: admin
Password: hg2x0123456
```

然后执行：

```sh
su root
```

输入前面计算的 root 密码。成功后检查：

```sh
id
uname -m
df -h /data
```

预期：

- `id` 显示 `uid=0(root)`。
- `uname -m` 显示 `aarch64`。
- `/data` 有可写空间。

如果认证失败，先核对 MAC、大小写和 key 计算，不要连续尝试大量密码。不同固件可能使用不同认证方式。

> Telnet 是明文协议，只应在可信局域网中使用。本文不假定 Telnet 开关在重启后仍然有效，也不包含未经验证的关闭方法。

## 安装程序和配置

以下命令均在 CPE 的 root shell 中执行。

### 1. 创建安全目录并上传二进制

```sh
umask 077
mkdir -p /data/fhconf/sms-forwarder
chmod 700 /data/fhconf/sms-forwarder
```

把 `<电脑IP>` 替换为运行 Python HTTP 服务的电脑地址：

```sh
curl -fsS -m 30 \
  -o /data/fhconf/sms-forwarder/sms-forwarder.new \
  http://<电脑IP>:18080/sms-forwarder-arm64

chmod 755 /data/fhconf/sms-forwarder/sms-forwarder.new
mv /data/fhconf/sms-forwarder/sms-forwarder.new \
   /data/fhconf/sms-forwarder/sms-forwarder
```

安装目录选择 `/data/fhconf/sms-forwarder`，不要放在 `/data` 根目录或其他可能被原厂清理的位置。二进制小于 30 MiB，日志也有大小限制。

### 2. 写入飞书配置

将两个占位符替换成自己的值：

```sh
cat > /data/fhconf/sms-forwarder/config <<'EOF'
FEISHU_WEBHOOK='https://open.feishu.cn/open-apis/bot/v2/hook/REPLACE_ME'
FEISHU_SECRET='REPLACE_ME'
EOF

chmod 600 /data/fhconf/sms-forwarder/config
```

`config` 只保存在 CPE 本地，不应复制到公开仓库。

### 3. 创建运行包装脚本

```sh
cat > /data/fhconf/sms-forwarder/run.sh <<'EOF'
#!/bin/sh
set -e
. /data/fhconf/sms-forwarder/config
export FEISHU_WEBHOOK FEISHU_SECRET
exec /data/fhconf/sms-forwarder/sms-forwarder \
  -log-file /data/fhconf/sms-forwarder/forwarder.log
EOF

chmod 700 /data/fhconf/sms-forwarder/run.sh
```

包装脚本让 procd 的服务状态中不直接显示 Webhook 和签名密钥。

### 4. 创建 procd 服务定义

```sh
cat > /data/fhconf/sms-forwarder/sms-forwarder.init <<'EOF'
#!/bin/sh /etc/rc.common
START=99
USE_PROCD=1

start_service() {
    procd_open_instance
    procd_set_param command /data/fhconf/sms-forwarder/run.sh
    procd_set_param respawn
    procd_set_param stderr 1
    procd_close_instance
}
EOF

chmod 700 /data/fhconf/sms-forwarder/sms-forwarder.init
```

### 5. 创建统一启停脚本

```sh
cat > /data/fhconf/sms-forwarder/start.sh <<'EOF'
#!/bin/sh
set -e
exec /etc/rc.common /data/fhconf/sms-forwarder/sms-forwarder.init start
EOF

cat > /data/fhconf/sms-forwarder/stop.sh <<'EOF'
#!/bin/sh
set -e
exec /etc/rc.common /data/fhconf/sms-forwarder/sms-forwarder.init stop
EOF

chmod 700 /data/fhconf/sms-forwarder/start.sh
chmod 700 /data/fhconf/sms-forwarder/stop.sh
```

检查脚本语法：

```sh
sh -n /data/fhconf/sms-forwarder/run.sh
sh -n /data/fhconf/sms-forwarder/sms-forwarder.init
sh -n /data/fhconf/sms-forwarder/start.sh
sh -n /data/fhconf/sms-forwarder/stop.sh
```

## 初始化、测试和启动

### 1. 初始化历史短信基线

```sh
. /data/fhconf/sms-forwarder/config
export FEISHU_WEBHOOK FEISHU_SECRET

/data/fhconf/sms-forwarder/sms-forwarder -once -ubus=false
```

预期看到类似：

```text
initialized SMS baseline with 243 existing files
```

数字取决于设备已有短信数量。这个步骤只记录现有文件，**不会把历史短信推送到飞书**。

状态保存在：

```text
/data/fhconf/sms-forwarder/state.json
```

### 2. 测试飞书发送

```sh
/data/fhconf/sms-forwarder/sms-forwarder \
  -text '【CPE SMS】安装测试'
```

看到“飞书消息发送成功”后，检查飞书群是否收到。

本验证设备的 Unix 时间快 8 小时，所以程序默认使用 `-timestamp-offset=-8h` 修正签名。在时钟准确的系统上运行时，应显式使用：

```sh
-timestamp-offset=0
```

### 3. 启动服务

```sh
/data/fhconf/sms-forwarder/start.sh
```

检查进程：

```sh
pidof sms-forwarder
ps w | grep -E '[u]bus.*listen|[s]ms-forwarder'
```

应该同时看到转发器和：

```text
/bin/ubus listen cpe_new_sms_notify
```

查看日志：

```sh
tail -f /data/fhconf/sms-forwarder/forwarder.log
```

预期启动日志：

```text
SMS forwarder started; watching /data/fhconf/sms
```

重复执行 `start.sh` 不会启动多个转发器进程。

### 4. 验证真实短信

给 CPE 中的 SIM 卡发送一条测试短信。飞书收到的新消息格式为：

```text
【CPE SMS】新短信
发件人：10086
时间：2026/10/29 02:01:00

短信正文
```

日志中会出现：

```text
forwarded SMS <文件名> from <发件人>
```

同一条短信重复扫描不会重复推送。

## 日常管理

启动：

```sh
/data/fhconf/sms-forwarder/start.sh
```

停止：

```sh
/data/fhconf/sms-forwarder/stop.sh
```

检查运行状态：

```sh
pidof sms-forwarder
ps w | grep -E '[u]bus.*listen|[s]ms-forwarder'
```

查看日志：

```sh
tail -f /data/fhconf/sms-forwarder/forwarder.log
```

查看最近的短信文件：

```sh
ls -lt /data/fhconf/sms | head
```

## 重启和崩溃行为

### 进程崩溃

procd 会在本次系统运行期间自动拉起转发器，ubus 监听也会随之恢复。该行为已通过主动终止进程验证。

### CPE 重启

**当前没有可靠的无人值守开机自启动。**

procd 的运行时注册不会跨重启保留，而且验证中没有找到不修改 rootfs、又能保证每次开机执行 `/data` 自定义程序的通用入口。因此重启后需要重新执行：

```sh
/data/fhconf/sms-forwarder/start.sh
```

如果 Telnet 已无法连接，需要先按前文重新开启 Telnet。

`state.json` 和短信文件位于持久分区。服务停止期间到达的短信，会在下次启动后按文件扫描补发，但仍以实际固件保留这些文件为前提。

不要把运行时 `respawn` 误认为已经完成开机自启动。

## 常用参数

完整参数可执行以下命令查看：

```sh
/data/fhconf/sms-forwarder/sms-forwarder -h
```

主要参数：

- `-webhook`：飞书 Webhook，也可用 `FEISHU_WEBHOOK`。
- `-secret`：签名密钥，也可用 `FEISHU_SECRET`。
- `-ca-file`：附加 CA 证书包，也可用 `FEISHU_CA_FILE`。
- `-timestamp-offset`：飞书签名时间偏移，本设备默认 `-8h`。
- `-text`：只发送一条文本后退出，不启动转发器。
- `-once`：只扫描一次后退出，常用于基线初始化。
- `-ubus=false`：关闭 ubus 监听，只使用文件扫描。
- `-sms-dir`：短信目录，默认 `/data/fhconf/sms`。
- `-metadata-dir`：短信元数据目录。
- `-state-file`：去重状态，默认 `/data/fhconf/sms-forwarder/state.json`。
- `-log-file`：日志文件；不指定时输出到标准错误。
- `-poll-interval`：定期对账间隔，默认 `1m`。
- `-retry-interval`：失败重试间隔，默认 `15s`。
- `-settle-delay`：新文件写入稳定等待时间，默认 `500ms`。
- `-timeout`：飞书请求超时，默认 `15s`。

## 故障排查

### 飞书签名失败

先比较 CPE 和电脑的 Unix 时间：

```sh
date '+%Y-%m-%d %H:%M:%S %Z %s'
```

本验证设备比电脑快 28800 秒，因此使用默认 `-8h`。如果时间已经同步，改用 `-timestamp-offset=0`。

### 飞书返回 HTTP 或 API 错误

- 检查 Webhook 是否完整。
- 检查签名密钥是否正确。
- 如果机器人配置了关键词，检查消息是否包含 `CPE SMS`。
- 查看 `forwarder.log` 中的具体错误，程序不会把 API 失败当成发送成功。

### 没有收到新短信通知

依次检查：

```sh
pidof sms-forwarder
ps w | grep -E '[u]bus.*listen|[s]ms-forwarder'
ls -lt /data/fhconf/sms | head
tail -n 50 /data/fhconf/sms-forwarder/forwarder.log
```

确认：

- 服务正在运行。
- ubus 监听子进程存在。
- CPE 确实生成了新的短信文件。
- 日志没有飞书连接或解析错误。

### HTTPS CA 错误

程序支持通过 `-ca-file` 添加正确的 CA 证书包，不建议关闭证书校验。当前验证设备的 Go 程序已能直接完成飞书 HTTPS 请求，不需要额外 CA 文件。

### 服务重启后没有运行

这是当前已知限制，不是短信解析故障。手动执行：

```sh
/data/fhconf/sms-forwarder/start.sh
```

## 安全说明

- 不要将真实 Webhook、签名密钥、Telnet 密码或个人短信内容提交到仓库。
- `config` 为 `0600`，安装目录为 `0700`，脚本为 `0700`。
- 飞书生产请求始终使用 HTTPS 和证书校验，不使用 `curl -k`。
- Telnet 是明文协议，只在可信局域网中使用。
- 程序不读取 Web 超管密码，不调用未认证敏感节点，不使用命令注入。
- 不修改原厂短信、网络、启动或分区配置。

## 已知限制

- 仅验证了 LG6121F COMMON/INTL 固件及其实际文件格式。
- 当前实机短信元数据编码均为 `8`；其他固件和编码需要重新验证。
- ubus 事件不持久化，不能单独保证不丢事件，因此必须保留文件补扫。
- 重启后的无人值守自启动尚未实现。
- 恢复出厂、固件升级或原厂清理操作是否保留安装目录，不在本方案保证范围内。
- 本程序只负责接收并转发短信，不提供发送短信、双向控制或群成员管理。

## 源码结构

```text
main.go       命令行、运行模式和参数
feishu.go     飞书 Webhook、签名、CA 和错误处理
sms.go        短信文件、正文解码和元数据
forwarder.go  扫描、稳定等待、发送和重试
state.go      历史基线、内容哈希和原子状态写入
ubus.go       ubus 监听、事件唤醒和断线重连
logging.go    日志权限和大小轮转
go.mod        Go 模块定义，无第三方依赖
```
