<p align="center">
  <img src="docs/logo/understudy.svg" alt="understudy" width="120" height="120">
</p>

<h1 align="center">understudy</h1>

<p align="center"><b>コーディングエージェントのためのモックAIモデル：トークン消費を抑え、エージェントテストを決定論的に実行。</b></p>

<p align="center">
  <a href="README.md">English</a> | <a href="README.ko.md">한국어</a> | <b>日本語</b> | <a href="README.zh-CN.md">简体中文</a>
</p>

## 概要

Claude Code、Codex CLI、opencode、pi、Gemini CLI、Antigravity CLIなどのコーディングエージェントは、HTTP API経由でLLMモデルと通信します。**understudy**は、簡潔なYAMLスクリプトを使ってこれらのモデルAPIをモック（模倣）します。各ターンでモデルが返すレスポンスを定義し、実際のエージェントの接続先をunderstudyに向けるだけで、エージェントは通常どおりファイルの読み書き、コマンド実行、リトライ、ストリーミング処理を実環境そのままに行います。

本物のモデルやAPIキーを一切使用しないため、エージェントテストはミリ秒単位で高速に実行され、常に同じ結果（決定論的実行）が得られます。さらに、プロバイダーの実APIと同じステータスコードとペイロードで、HTTPエラー、遅延（ストール）、切断、レート制限を正確にシミュレートできます。

<p align="center">
  <a href="docs/screenshots/hero-divergence.png"><img src="docs/screenshots/hero-divergence.png" width="880" alt="Web UIの不一致表示：実際の要求がスクリプトのどのケースにも一致せず、期待値と受信値が並べて表示されている様子"></a>
</p>

## クイックスタート

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

understudy serve --script hello.yaml &         # Web UI: http://127.0.0.1:8765/_understudy/ui/
eval "$(understudy env)"                       # Claude Codeの接続先をダミーキー付きでunderstudyに設定
claude -p "say hi" </dev/null                  # 出力: Hello from the understudy.
understudy explain --url http://127.0.0.1:8765 # 各リクエストに応答したステップとその理由を表示
```

スクリプトではツールの呼び出し、コマンド結果に基づく分岐、リトライ処理、エラーの注入も可能です。詳細な仕様は [SCRIPTS.md](SCRIPTS.md) をご覧ください。`understudy validate FILE` を実行すると、スクリプトの構文チェックと修正提案を確認できます。

## 他のエージェントハーネスでの利用

`understudy setup` は、隔離されたホームディレクトリにハーネスの設定ファイルを生成し、ダミーキーでunderstudyに対して実行するコマンドを出力します（`--session KEY` でセッション名を指定できます）。

| ハーネス | 設定方法 |
|---|---|
| Claude Code | `eval "$(understudy env)"` |
| OpenAI Codex CLI | `understudy setup --harness codex --home /tmp/h` |
| opencode | `understudy setup --harness opencode --home /tmp/h` |
| pi | `understudy setup --harness pi --home /tmp/h` |
| Gemini CLI | `understudy setup --harness gemini-cli --home /tmp/h` |
| Antigravity CLI | `understudy setup --harness antigravity --home /tmp/h` |

対応プロトコル、生成される設定ファイル、動作検証済みCLIバージョンの一覧は [docs/harnesses.md](docs/harnesses.md) を参照してください。

## Web UI と MCP

`understudy serve` は `/_understudy/ui/` でリアルタイムWeb UIを提供します。稼働中セッションの監視、各ターンに応答したステップの追跡、予期しない不一致（divergence）の検証、一時停止中ゲートの手動解除、読み込まれたスクリプトの確認などをブラウザ上で行えます（[詳細](docs/web-ui.md)）。

`understudy mcp` は、Model Context Protocol（MCP）ツールを通じてこれらの機能をAIエージェントに提供します。エージェント自身がスクリプトの作成・検証、ハーネスCLIの実行、リクエストログの確認、ゲートの解除を行えます（[詳細](docs/mcp.md)）。

<table>
  <tr>
    <td width="50%" valign="top"><a href="docs/screenshots/sessions.png"><img src="docs/screenshots/sessions.png" width="100%" alt="セッション一覧：プランナー、ライター、レビュアー、テスターの12セッション、ゲート停止、不一致、ストリーミング状態を表示"></a><br><sub><b>セッション管理</b>: セッション状態、ターン数、ステップ数の確認と停止中ゲートの解除</sub></td>
    <td width="50%" valign="top"><a href="docs/screenshots/requests.png"><img src="docs/screenshots/requests.png" width="100%" alt="リクエスト一覧：41件のリクエストログと不一致リクエストの詳細比較"></a><br><sub><b>リクエストログ</b>: 全リクエストログの閲覧とペイロード差分の並列比較</sub></td>
  </tr>
  <tr>
    <td width="50%" valign="top"><a href="docs/screenshots/conversation.png"><img src="docs/screenshots/conversation.png" width="100%" alt="セッション詳細：プロンプト、思考プロセス、スクリプトによるRead呼び出し、Claude Codeからのツール結果"></a><br><sub><b>対話履歴</b>: プロンプト、スクリプト応答、クライアントツール結果の確認</sub></td>
    <td width="50%" valign="top"><a href="docs/screenshots/script.png"><img src="docs/screenshots/script.png" width="100%" alt="スクリプト画面：シンタックス強調されたYAMLとステップごとの応答回数"></a><br><sub><b>スクリプトビューア</b>: 読み込まれたYAMLスクリプトとステップごとのリアルタイム実行回数</sub></td>
  </tr>
  <tr>
    <td colspan="2"><a href="docs/screenshots/dark-mobile.png"><img src="docs/screenshots/dark-mobile.png" width="100%" alt="ダークモードおよび幅390pxのモバイル画面における修正ループとゲート停止中のレビュアー"></a><br><sub><b>モバイル＆ダークモード</b>: ダークモードおよびモバイル画面への完全対応</sub></td>
  </tr>
</table>

## ドキュメント一覧

- [SCRIPTS.md](SCRIPTS.md): スクリプト形式の仕様、スキーマリファレンス、完全な設定例
- [機能概要](docs/features.md): 対応プロトコル、障害注入、トラフィック記録、Goテストヘルパー
- [ハーネス設定ガイド](docs/harnesses.md): 各ハーネスの設定手順と検証済みバージョン一覧
- [障害・使用量制限](docs/limits.md): APIエラーやレート制限に対する各ハーネスの実測挙動
- [記録と再生](docs/recording.md) · [Goライブラリ](docs/go.md) · [コントロールAPI](docs/control-api.md)
- [Web UI](docs/web-ui.md) · [MCPサーバー](docs/mcp.md) · [テスト手法](docs/testing.md) · [アーキテクチャ](docs/ARCHITECTURE.md)
- [参考プロジェクト](docs/credits.md) · [コントリビューション](CONTRIBUTING.md)

## ライセンス

Apache-2.0. 「Claude」および「Anthropic」は Anthropic の商標です。「OpenAI」および「Codex」は OpenAI の商標です。「Gemini」および「Antigravity」は Google の商標です。「DeepSeek」は DeepSeek の商標です。understudy はこれらの組織と提携または承認されているものではありません。
