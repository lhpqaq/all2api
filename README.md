# all2api

**all2api** 是一个用 Go 编写的 API 网关。它的目标是把不同上游服务（目前支持 Cursor 和 Zed）的非标准化 API 接口，统一转换为常见的标准下游协议（如 OpenAI 的 Chat Completions 接口）。

## ✨ 特性

- 🔌 **标准 API 支持**：将复杂的上游服务转化为标准的 OpenAI (`/v1/chat/completions`, `/v1/models`) 和 Anthropic (`/v1/messages`) 接口。
- 🛠️ **工具调用 (Tool Calls)**：支持在上游不支持原生 Tool Calls 的情况下，通过提示词注入（Prompt Injection）和解析来完美模拟函数调用。
- 📦 **多上游管理**：支持单一网关聚合多个上游，允许为每个上游独立配置鉴权、HTTP 代理和超时功能。
- ⌨️ **便捷命令**：提供自动获取 Token 并无损写入配置文件的快捷命令，简化繁琐的人工操作。

---

## 🚀 使用方法

### 1. 运行服务

确保您已安装 Go 1.22+，在项目根目录运行或编译：

```bash
cd all2api
go mod tidy

# 运行服务
go run ./cmd/all2api -config ./config.yaml
```

系统默认监听于 `127.0.0.1:8848`。可以通过 `/v1/models` 获取动态加载的模型列表。

### 2. 账号鉴权与自动配置

本项目提供命令行快捷自动登录上游应用（并自动更新本地 `config.yaml`）。以 **Zed** 为例：
```bash
go run ./cmd/all2api login zed
```
执行命令后将唤起浏览器，授权成功后会自动拦截获取 Token，并无损地将配置注入您的 `config.yaml`，保留原有注释。

### 3. 给上游单独设置代理

如果您需要为特定上游指定单独的 HTTP 代理（而非使用全局环境变量），可以在 `config.yaml` 的上游节点增加 `proxy` 配置：

```yaml
upstreams:
  zed:
    proxy: "http://127.0.0.1:7890"  # 单独为 Zed 配置代理
    auth:
      token: "..."
  cursor:
    proxy: "socks5://127.0.0.1:1080"
```

---

## 🐳 Docker 部署

### 快速启动

1. **启动服务**

   ```bash
   docker compose up -d
   ```

   首次启动时，若 `./config/config.yaml` 不存在，容器会自动从内置的 `config.yaml.example` 复制生成一份。

   服务监听 `0.0.0.0:8848`，验证：

   ```bash
   curl http://localhost:8848/v1/models
   ```

2. **编辑配置（首次启动后）**

   ```bash
   # 自动生成的配置文件在宿主机的 ./config/config.yaml
   vim config/config.yaml   # 填入 Token 等鉴权信息
   docker compose restart
   ```

3. **仅用 Docker 启动（不使用 Compose）**

   ```bash
   # 构建镜像
   docker build -t all2api:latest .

   # 运行容器
   docker run -d \
     --name all2api \
     --restart unless-stopped \
     -p 8848:8848 \
     -v $(pwd)/config:/app/config \
     -e ALL2API_DEBUG=true \
     all2api:latest
   ```

### 配置说明

| 说明 | 路径/环境变量 |
|------|---------------|
| 配置文件挂载目录 | `./config/config.yaml`（首次启动自动生成，可直接编辑）|
| 开启 debug 日志 | 环境变量 `ALL2API_DEBUG=true`（默认已开启）或请求头 `X-All2API-Debug: 1` |
| 全局 HTTP 代��� | 环境变量 `HTTP_PROXY` / `HTTPS_PROXY` |
| 单独上游代理 | `config.yaml` 中对应上游的 `proxy` 字段 |

> **注意**：`./config/config.yaml` 自动生成后默认无有效 Token，需手动填入鉴权信息后重启容器才能正常使用。

---

## 📝 Zed 的注意事项

- 🛸 **网络代理**：所在地不支持 Claude 模型，需要单的配置网络代理。

---

## 🤝 致敬 (Acknowledgments)

本项目的诞生离不开开源社区无私的分享。我们在逆向分析上游流媒体格式和模型调用逻辑上，深受以下优秀的开源项目启发，向原作者表示诚挚的致敬与感谢：

- [zed2api](https://github.com/yukmakoto/zed2api) by [@yukmakoto](https://github.com/yukmakoto)
- [cursor2api](https://github.com/7836246/cursor2api) by [@7836246](https://github.com/7836246)

---

## ⚠️ 免责声明

1. 本项目作为开源的网关中间件代码参考，**仅应用于您的个人学习、研究和代码交流**，请勿用于商业生产。
2. 请严格遵守目标上游服务商（Cursor、Zed 等第三方软件）的终端用户服务协议（EULA）。**严禁借由本项目的能力进行商业倒卖、公共共享以及任何可能构成滥用并发访问权限的灰黑产行径**。
3. 您在部署并使用本项目所提供的功能和接口时，需自行承担一切潜在风险。因您非法滥用本项目导致您的原始账户被封禁、资源被禁用，或由此产生任何民事诉讼、赔偿责任，**本项目及其开发者概不负责，也与其无任何连带法律关联**。
