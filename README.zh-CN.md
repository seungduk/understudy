<p align="center">
  <img src="docs/logo/understudy.svg" alt="understudy" width="120" height="120">
</p>

<h1 align="center">understudy</h1>

<p align="center"><b>面向编程智能体的假 AI 模型：节省 token，让智能体测试每次都得到相同的结果。</b></p>

<p align="center">
  <a href="README.md">English</a> | <a href="README.ko.md">한국어</a> | <a href="README.ja.md">日本語</a> | <b>简体中文</b>
</p>

## 简介

Claude Code、Codex 等编程智能体会通过 HTTP API 把每个请求发给 AI 模型。**understudy** 是一个假模型：它是在你本机运行的服务器，按一份简短的 YAML 脚本回答这些请求。你在脚本里写好假模型每一步要说什么，把智能体指向 understudy，然后运行真实的智能体。只有模型是假的：智能体照常读取和编辑文件、运行命令、重试，并以流式方式接收回复。

因此，智能体测试不需要 API 密钥，不消耗 token，每次运行结果都相同。假模型还可以用 HTTP 错误、响应停顿（stall）、连接中断和用量上限来回答，并返回与真实 API 相同的错误正文和状态码。各智能体在每种故障下的实测表现见 [docs/limits.md](docs/limits.md)。

<p align="center">
  <a href="docs/screenshots/hero-divergence.png"><img src="docs/screenshots/hero-divergence.png" width="880" alt="Web UI 的偏离页面：智能体发出的请求与脚本不符，与脚本的预期内容并排显示"></a>
</p>

## 示例：测试智能体是否改了文件

在 [examples/2-edit-file.yaml](examples/2-edit-file.yaml) 中，假模型让智能体先读取 `greeting.txt`，再把 "hello" 换成 "goodbye"。下面的 Go 测试用这份脚本运行真实的 Claude Code，并检查磁盘上的文件：

```go
func TestAgentEditsTheFile(t *testing.T) {
	fake := us.Start(t, us.ScriptFile("testdata/edit.yaml")) // examples/2-edit-file.yaml 的副本
	dir := us.TempDir(t, map[string]string{"greeting.txt": "hello world\n"})

	run := us.Claude(t, fake, us.Run{Dir: dir, Session: "t1/writer", Prompt: "Change the greeting to goodbye."})

	got, _ := os.ReadFile(filepath.Join(dir, "greeting.txt"))
	if run.Exit != 0 || string(got) != "goodbye world\n" {
		t.Fatalf("exit %d, greeting.txt is %q", run.Exit, got)
	}
}
```

`us` 即 `github.com/seungduk/understudy/understudytest`（用 `go get github.com/seungduk/understudy/understudytest@latest` 添加）。未安装 `claude` 时，该测试会被跳过。用其他智能体写同样的测试，见 [docs/go.md](docs/go.md)。

## 前置条件

- Go 1.25 或更高版本。目前还没有预编译的二进制文件。
- 要测试的智能体，已安装并在 `PATH` 中。快速开始使用 Claude Code（`claude`）。

## 快速开始

```sh
go install github.com/seungduk/understudy/cmd/understudy@latest

cat > hello.yaml <<'YAML'
schema: understudy/v1
sessions:
  default:
    steps:
      - name: answer
        reply: {message: {text: "Hello from the understudy."}}
YAML

understudy serve --script hello.yaml &         # Web UI：http://127.0.0.1:8765/_understudy/ui/
eval "$(understudy env)"                       # 当前 shell 中的 Claude Code 用假密钥连接 understudy
claude -p "say hi" </dev/null                  # 输出：Hello from the understudy.
understudy explain --url http://127.0.0.1:8765 # 显示每个请求由哪个脚本步骤回答，以及原因
```

`understudy env` 只在当前 shell 中清除你真实的 Anthropic 密钥；要重新使用真实模型，请打开一个新 shell。脚本还可以调用工具、根据命令输出分支、返回错误，详见 [SCRIPTS.md](SCRIPTS.md)。`understudy validate FILE` 会检查脚本，并对每个问题给出修改方法。

## 本文用语

- **会话（session）**：脚本中的一段对话，用 `t1/writer` 这样的键命名。智能体在每个请求中发送这个键；没有键的请求归入 `default` 会话。
- **步骤（step）**：脚本里的一条回答。第 1 步回答会话的第一个请求，第 2 步回答下一个，依此类推。
- **偏离（divergence）**：脚本没有预料到的请求。默认情况下，understudy 用错误来回答，并把脚本的预期与智能体实际发送的内容并排显示。
- **闸门（gate）**：一个有名字的暂停点。带有 `stall: {gate: NAME}` 的步骤会一直不回答，直到你放行这个闸门。

## 配合其他智能体使用

`understudy setup` 把智能体的配置文件写进一个空文件夹（下面用 `./agent-home`），让它成为智能体的主目录，然后打印一条命令：用假密钥运行智能体并连接 understudy。加上 `--session KEY` 可以选择会话。

| 智能体 | 设置命令 |
|---|---|
| Claude Code | `eval "$(understudy env)"` |
| OpenAI Codex CLI | `understudy setup --harness codex --home ./agent-home` |
| opencode | `understudy setup --harness opencode --home ./agent-home` |
| pi | `understudy setup --harness pi --home ./agent-home` |
| Gemini CLI | `understudy setup --harness gemini-cli --home ./agent-home` |
| Antigravity CLI | `understudy setup --harness antigravity --home ./agent-home` |

各智能体使用的 API、`setup` 写入的文件，以及我们测试过的智能体版本，见 [docs/harnesses.md](docs/harnesses.md)。

## Web UI 与 MCP

`understudy serve` 还在 `/_understudy/ui/` 提供 Web UI。它显示每个会话的状态、回答每个请求的步骤、每次偏离以及已加载的脚本，并提供放行闸门的按钮（[了解更多](docs/web-ui.md)）。

`understudy mcp` 以 Model Context Protocol（MCP）工具的形式把同样的功能提供给 AI 智能体：它们可以编写和检查脚本、获取让智能体连接 understudy 运行的命令、读取请求日志，以及放行闸门（[了解更多](docs/mcp.md)）。

<table>
  <tr>
    <td width="50%" valign="top"><a href="docs/screenshots/sessions.png"><img src="docs/screenshots/sessions.png" width="100%" alt="会话页面：规划、编写、审查、测试工作流中的 12 个会话，包括停在闸门处的会话、发生偏离的会话和正在流式传输的会话"></a><br><sub><b>会话</b>：每个会话的状态、当前步骤和请求数；可在此放行停住的闸门。</sub></td>
    <td width="50%" valign="top"><a href="docs/screenshots/requests.png"><img src="docs/screenshots/requests.png" width="100%" alt="请求页面：41 个请求的日志，旁边打开了一个偏离的请求"></a><br><sub><b>请求</b>：智能体发出的每个请求；打开一个，即可与脚本的预期并排对照。</sub></td>
  </tr>
  <tr>
    <td width="50%" valign="top"><a href="docs/screenshots/conversation.png"><img src="docs/screenshots/conversation.png" width="100%" alt="会话详情：初始提示词、模型思考、脚本中的 Read 调用，以及 Claude Code 的工具输出"></a><br><sub><b>会话记录</b>：提示词、假模型按脚本给出的回答，以及智能体返回的工具结果。</sub></td>
    <td width="50%" valign="top"><a href="docs/screenshots/script.png"><img src="docs/screenshots/script.png" width="100%" alt="脚本页面：语法高亮的 YAML，以及每个步骤回答过的请求数"></a><br><sub><b>脚本</b>：已加载的 YAML 脚本，以及每个步骤至今回答过的请求数。</sub></td>
  </tr>
  <tr>
    <td colspan="2"><a href="docs/screenshots/dark-mobile.png"><img src="docs/screenshots/dark-mobile.png" width="100%" alt="手机宽度屏幕上的深色模式：编写者的修复循环，以及在闸门处等待的审查者"></a><br><sub><b>手机与深色模式</b>：Web UI 在窄屏和深色模式下同样可用。</sub></td>
  </tr>
</table>

## 了解更多

- [SCRIPTS.md](SCRIPTS.md)：脚本格式、所有字段和实际示例
- [功能](docs/features.md)：API、故障、录制真实流量，以及 Go 测试辅助工具
- [智能体](docs/harnesses.md)：每个智能体的设置方法，以及测试过的版本
- [故障与上限](docs/limits.md)：各智能体在 API 错误、响应停顿和用量上限下的实测表现
- [录制与重放](docs/recording.md) · [Go 库](docs/go.md) · [控制 API](docs/control-api.md)
- [Web UI](docs/web-ui.md) · [MCP 服务器](docs/mcp.md) · [测试](docs/testing.md) · [架构](docs/ARCHITECTURE.md)
- [致谢与参考](docs/credits.md) · [贡献指南](CONTRIBUTING.md)

## 许可证

Apache-2.0。“Claude” 和 “Anthropic” 是 Anthropic 的商标；“OpenAI” 和 “Codex” 是 OpenAI 的商标；
“Gemini” 和 “Antigravity” 是 Google 的商标；“DeepSeek” 是 DeepSeek 的商标。understudy 与上述任何一方均无关联，
也未获得其认可。
