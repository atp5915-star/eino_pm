# 文心健康管家 Demo

基于 [Eino](https://github.com/cloudwego/eino) 框架搭建的移动端 AI 健康助手 Demo。

![demo preview](demo/preview.png)

---

## 快速开始（PM 同事看这里）

### 第 1 步：装 Go 环境

打开终端（Terminal），输入：

```bash
brew install go
```

如果没装过 Homebrew，先到 [brew.sh](https://brew.sh/) 装一下。

> 检查是否成功：`go version`，看到版本号就 OK。

### 第 2 步：克隆代码

```bash
git clone https://github.com/atp5915-star/eino_pm.git
cd eino_pm
```

### 第 3 步：配置 API Key

```bash
cp .env.example .env
```

然后用任意编辑器（比如 VS Code、TextEdit）打开 `.env` 文件，把下面三行的占位符替换为真实值：

```
OPENAI_BASE_URL=https://your-oneapi-host/v1
OPENAI_API_KEY=sk-你的key
OPENAI_MODEL=gpt-4o
```

> **API Key 从哪儿拿？** 找张文要 OneAPI 网关地址和 Key。

### 第 4 步：启动

```bash
./run.sh
```

第一次启动会自动下载依赖（一两分钟），看到这行说明成功了：

```
starting server on http://localhost:8080
```

打开浏览器访问 **http://localhost:8080** 就能看到 demo。

---

## 常见问题

**Q: 启动报 `command not found: go`**
A: Go 没装好，重新执行 `brew install go`。

**Q: 启动报 `permission denied: ./run.sh`**
A: 执行 `chmod +x run.sh` 给脚本加权限。

**Q: 浏览器打开是空白页**
A: 检查终端日志有没有报错；常见是 API Key 没填对，或者 BASE_URL 写错了。

**Q: 想换端口**
A: 在 `.env` 里加一行 `PORT=9090`。

**Q: 想用火山方舟（ARK）模型**
A: 在 `.env` 里加：
```
MODEL_TYPE=ark
ARK_API_KEY=...
ARK_BASE_URL=...
ARK_MODEL=...
```

---

## 仓库结构

```
eino_pm/
├── README.md              ← 你正在看的这个
├── .env.example           ← 环境变量模板
├── run.sh                 ← 一键启动脚本
├── demo/                  ← 纯前端 demo（双击 index.html 可单独预览 UI）
│   └── index.html
├── eino-examples/         ← Eino 官方示例（含 chatwitheino 后端服务）
│   └── quickstart/chatwitheino/  ← 我们用的服务
├── eino/                  ← Eino 框架源码（仅供查阅，不影响运行）
├── SKILL-SPEC.md          ← Skill 规范文档
└── SKILL-GUIDE.md         ← Skill 使用指南
```

`demo/index.html` 是前端页面源文件；`eino-examples/quickstart/chatwitheino/static/index.html` 是服务实际加载的同一份页面（通过 `cp` 同步）。

---

## 其他

- 后端：Go + Hertz + Eino
- 前端：纯 HTML/CSS/JS（无构建系统）
- 流式输出：SSE
- 默认模型：OpenAI 兼容协议（OneAPI / OpenAI / 火山方舟）
