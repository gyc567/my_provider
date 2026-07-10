# systemd 启动情况检查报告

> 检查时间：2026-07-10

## 1. 当前生产环境：使用 systemd

已发现生产环境通过 systemd 管理 `my-provider` 服务：

- **服务文件**：`/etc/systemd/system/my-provider.service`
- **是否启用开机自启**：`enabled` ✅
- **当前状态**：`active (running)` ✅
- **主进程 PID**：`495154`
- **启动的二进制**：`/usr/local/bin/my-provider`
- **工作目录**：`/root/code/my_provider`

### 服务文件内容

```ini
[Unit]
Description=T-0 Provider Service
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/root/code/my_provider
ExecStart=/usr/local/bin/my-provider
Restart=always
RestartSec=5
StartLimitInterval=60s
StartLimitBurst=3
StandardOutput=journal
StandardError=journal
SyslogIdentifier=my-provider

[Install]
WantedBy=multi-user.target
```

### 特点
- `Restart=always`：进程崩溃后自动重启
- `RestartSec=5`：重启间隔 5 秒
- `StartLimitBurst=3`：60 秒内最多启动 3 次，防止频繁重启
- 日志输出到 systemd journal，可通过 `journalctl -u my-provider.service -f` 查看

## 2. 部署脚本 `scripts/deploy-local.sh`：不使用 systemd

当前脚本使用 `nohup` 在后台直接启动进程：

```bash
nohup "$BINARY" > "$LOG_FILE" 2>&1 &
local_pid=$!
echo "$local_pid" > "$PID_FILE"
```

### 特点
- 将二进制启动在 `bin/my-provider`
- 日志写入 `logs/my-provider.log`
- PID 写入 `logs/my-provider.pid`
- 通过 `pkill` 和 PID 文件停止进程
- **不经过 systemd 管理**

## 3. 两者对比

| 维度 | systemd 服务 | `deploy-local.sh` 脚本 |
|------|-------------|------------------------|
| 启动方式 | `systemctl start my-provider.service` | `nohup bin/my-provider &` |
| 二进制位置 | `/usr/local/bin/my-provider` | `/root/code/my_provider/bin/my-provider` |
| 日志位置 | `journalctl -u my-provider.service` | `logs/my-provider.log` |
| 开机自启 | `enabled` ✅ | 无 |
| 崩溃自动重启 | `Restart=always` ✅ | 无 |
| 停止方式 | `systemctl stop my-provider.service` | `./scripts/deploy-local.sh --stop` |
| 适用场景 | 生产环境 | 本地开发/临时测试 |

## 4. 结论与建议

1. **生产环境当前已使用 systemd 启动**，服务名为 `my-provider.service`，并已设置开机自启。

2. **`scripts/deploy-local.sh` 并未使用 systemd**，它采用 `nohup` 后台进程方式，仅适合本地开发调试。

3. **当前存在不一致**：如果开发者运行 `./scripts/deploy-local.sh` 启动服务，会启动一个与 systemd 服务并行的独立进程，可能导致端口冲突或管理混乱。

### 已完成的优化

`scripts/deploy-local.sh` 已改造为支持两种模式：

```bash
./scripts/deploy-local.sh              # 本地开发：nohup 启动
./scripts/deploy-local.sh --systemd    # 生产部署：systemd 重启
./scripts/deploy-local.sh --stop       # 停止本地 nohup 进程
./scripts/deploy-local.sh --systemd --stop  # 停止 systemd 服务
```

`--systemd` 模式的行为：
1. 使用 `-ldflags` 注入 git commit 构建二进制
2. 安装到 `/usr/local/bin/my-provider`
3. 执行 `systemctl daemon-reload && systemctl restart my-provider.service`
4. 等待端口就绪并运行冒烟测试

### 建议

- **生产部署**：统一使用 `./scripts/deploy-local.sh --systemd`
- **本地开发**：使用 `./scripts/deploy-local.sh`（nohup 模式）
- **查看日志**：
  - 生产：`journalctl -u my-provider.service -f`
  - 本地：`tail -f logs/my-provider.log`
