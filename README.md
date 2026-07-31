# simple-lsp-mcp

ローカルの Language Server Protocol（LSP）サーバーを、AI エージェントから
MCP 経由で安全に利用するための、読み取り専用・シンボル優先のブリッジです。

コードをテキスト検索する代わりに、シンボル検索、定義、参照、呼び出し関係、型階層、診断を LSP に問い合わせます。ファイルの編集、シェルコマンドの実行、テキスト検索、永続的なソースインデックスの作成は行いません。

## 対応言語

| MCP の `language` | LSP 設定プロファイル | 主な拡張子 |
| --- | --- | --- |
| `python` | `python` | `.py` |
| `typescript`, `typescriptreact` | `typescript-javascript` | `.ts`, `.tsx` |
| `javascript`, `javascriptreact` | `typescript-javascript` | `.js`, `.jsx` |
| `go` | `go` | `.go` |
| `html` | `html` | `.html` |
| `css` | `css` | `.css` |

`language` と設定プロファイルは異なります。たとえば TypeScript のツール呼び出しでは `language: "typescript"` を指定し、`LSP_MCP_SERVERS` では `typescript-javascript` を設定します。

## 必要条件

- Go 1.26 以上（このリポジトリからビルドする場合）
- 利用する言語の LSP サーバー
- MCP クライアント（Codex または Claude Code など）

必要な LSP サーバーだけをインストール・設定してください。サーバーは最初にその言語を使うまで起動しません。

| 言語 | LSP サーバー | 代表的なインストール例 |
| --- | --- | --- |
| Python | `pyright-langserver` | `npm install -g pyright` |
| TypeScript / JavaScript | `typescript-language-server` | `npm install -g typescript typescript-language-server` |
| Go | `gopls` | `go install golang.org/x/tools/gopls@latest` |
| HTML / CSS | `vscode-html-language-server`, `vscode-css-language-server` | `npm install -g vscode-langservers-extracted` |

グローバルインストールを避ける場合、HTML/CSS の例のように設定で `npx` を起動できます。`command` と `args` はシェルを介さず直接プロセスに渡されるため、`~`、環境変数展開、パイプ、引用符による引数分割は使えません。

## インストール

公開済みのモジュールをインストールする場合:

```sh
go install github.com/tamutamu/simple-lsp-mcp/cmd/simple-lsp-mcp@latest
```

このリポジトリをチェックアウト済みの場合:

```sh
go build -o simple-lsp-mcp ./cmd/simple-lsp-mcp
```

生成したバイナリを MCP クライアントから実行できる場所に置くか、設定の `command` に絶対パスを指定します。

## 最短セットアップ

以下は Go と TypeScript を使うプロジェクトの設定例です。`/absolute/path/to/project` は解析対象のリポジトリの絶対パスに置き換えます。

```json
{
  "go": {"command": "gopls", "args": []},
  "typescript-javascript": {
    "command": "typescript-language-server",
    "args": ["--stdio"]
  }
}
```

この JSON を `LSP_MCP_SERVERS` 環境変数に渡し、サーバーにはワークスペースを指定します。

```sh
LSP_MCP_SERVERS='{"go":{"command":"gopls","args":[]},"typescript-javascript":{"command":"typescript-language-server","args":["--stdio"]}}' \
  simple-lsp-mcp --workspace /absolute/path/to/project
```

`--workspace` は必須です。MCP は標準入出力（stdio）で通信するため、通常は MCP クライアントから起動し、対話シェルで直接実行しません。

### 全プロファイルの設定例

```json
{
  "python": {"command": "pyright-langserver", "args": ["--stdio"]},
  "typescript-javascript": {
    "command": "typescript-language-server",
    "args": ["--stdio"]
  },
  "go": {"command": "gopls", "args": []},
  "html": {
    "command": "npx",
    "args": ["--yes", "--package=vscode-langservers-extracted", "vscode-html-language-server", "--stdio"]
  },
  "css": {
    "command": "npx",
    "args": ["--yes", "--package=vscode-langservers-extracted", "vscode-css-language-server", "--stdio"]
  }
}
```

使用しないプロファイルは削除して構いません。未設定の言語を呼ぶと `UNSUPPORTED_LANGUAGE` を返します。設定済みでも実行ファイルが見つからない場合は `LANGUAGE_SERVER_NOT_FOUND` を返します。

## Codex の設定

`~/.codex/config.toml` に追加します。`command` は `PATH` 上の名前か、ビルドしたバイナリの絶対パスにします。

```toml
[mcp_servers.simple-lsp]
command = "simple-lsp-mcp"
args = ["--workspace", "/absolute/path/to/project"]
enabled_tools = [
  "search_symbols", "list_workspace_symbols", "get_document_symbols", "get_symbol",
  "get_definition", "find_references", "find_implementations", "get_type_definition",
  "get_declaration", "get_incoming_calls", "get_outgoing_calls", "get_supertypes",
  "get_subtypes", "get_diagnostics"
]
default_tools_approval_mode = "approve"
startup_timeout_sec = 20
tool_timeout_sec = 45
enabled = true

[mcp_servers.simple-lsp.env]
LSP_MCP_SERVERS = """
{"go":{"command":"gopls","args":[]},"typescript-javascript":{"command":"typescript-language-server","args":["--stdio"]}}
"""
```

Codex からは、まず `search_symbols` または `get_document_symbols` を使ってシンボルを取得します。たとえば `src/greeting.ts` の関数一覧には、`get_document_symbols` に `path: "src/greeting.ts"` と `language: "typescript"` を指定します。

## Claude Code の設定

Claude Code の MCP 設定に追加します。

```json
{
  "mcpServers": {
    "simple-lsp": {
      "command": "simple-lsp-mcp",
      "args": ["--workspace", "/absolute/path/to/project"],
      "env": {
        "LSP_MCP_SERVERS": "{\"python\":{\"command\":\"pyright-langserver\",\"args\":[\"--stdio\"]},\"typescript-javascript\":{\"command\":\"typescript-language-server\",\"args\":[\"--stdio\"]},\"go\":{\"command\":\"gopls\",\"args\":[]},\"html\":{\"command\":\"npx\",\"args\":[\"--yes\",\"--package=vscode-langservers-extracted\",\"vscode-html-language-server\",\"--stdio\"]},\"css\":{\"command\":\"npx\",\"args\":[\"--yes\",\"--package=vscode-langservers-extracted\",\"vscode-css-language-server\",\"--stdio\"]}}"
      }
    }
  }
}
```

## MCP ツール

すべてのツールは結果を構造化データとして返します。シンボル・位置・範囲の行と列は **1 始まり**、パスはワークスペース相対です。

| ツール | 用途 | 必須入力 |
| --- | --- | --- |
| `search_symbols` | 名前でワークスペース内のシンボルを検索する | `query`, `language` |
| `list_workspace_symbols` | 指定言語のワークスペースシンボルを列挙する | `language` |
| `get_document_symbols` | 1 ファイルの階層的なシンボルを取得する | `path`, `language` |
| `get_symbol` | 既取得の `symbol_id` の詳細とソースを取得する | `symbol_id` |
| `get_definition` | 定義へ移動する | `language` と対象 |
| `find_references` | 参照箇所を取得する | `language` と対象 |
| `find_implementations` | 実装箇所を取得する | `language` と対象 |
| `get_type_definition` | 型定義へ移動する | `language` と対象 |
| `get_declaration` | 宣言へ移動する | `language` と対象 |
| `get_incoming_calls` | 直接の呼び出し元を取得する | `language` と対象 |
| `get_outgoing_calls` | 直接の呼び出し先を取得する | `language` と対象 |
| `get_supertypes` | 直接のスーパータイプを取得する | `language` と対象 |
| `get_subtypes` | 直接のサブタイプを取得する | `language` と対象 |
| `get_diagnostics` | ファイルの診断を取得する | `path` を渡す場合は `language` |

### 共通入力

- `language`: 上表の MCP 言語名。シンボル操作とファイル操作では必須です。
- `path`: ワークスペース相対のファイルパス。絶対パス、`..`、ワークスペース外を指すシンボリックリンクは拒否されます。
- `line`, `column`: 1 始まりの位置です。
- `limit`: 結果の上限。省略時は `--max-results` の値（既定 500）です。上限より大きい値は上限までに制限されます。
- `kinds`: `search_symbols` と `list_workspace_symbols` の種類フィルターです。例: `["function", "class"]`。

「対象」を受け取るツールでは、次のどちらか一方だけを指定します。

```json
{"symbol_id": "sym_..."}
```

```json
{"path": "src/main.go", "line": 12, "column": 8}
```

`symbol_id` は `search_symbols`、`list_workspace_symbols`、`get_document_symbols`、または階層ツールの結果から取得します。同じ MCP サーバープロセス内でのみ有効で、対応するファイルが変更された後は `STALE_SYMBOL` になります。`get_symbol` は既定でソースを含めます。不要な場合は `include_source: false` を指定してください。`find_references` は `include_declaration: true` を指定すると宣言も含めます。

LSP サーバーが要求された機能を広告していない場合、そのツールは `METHOD_NOT_SUPPORTED` を返します。対応状況は言語と LSP サーバーの実装によって異なります。

## 実行オプション

| オプション | 既定値 | 説明 |
| --- | --- | --- |
| `--workspace` | なし | 必須。解析対象ワークスペースのルート |
| `--log-level` | `info` | `debug`, `info`, `warn`, `error` |
| `--request-timeout` | `15s` | LSP リクエストのタイムアウト |
| `--diagnostics-wait` | `2s` | プッシュ型診断を待つ時間 |
| `--max-results` | `500` | 各ツールが返す結果数の上限 |

## トラブルシューティング

| 応答コード | 原因と対処 |
| --- | --- |
| `INVALID_PATH` | `path` がワークスペース相対であり、実在するファイルを指すか確認します。 |
| `UNSUPPORTED_LANGUAGE` | `language` が対応表の値であり、その設定プロファイルが `LSP_MCP_SERVERS` にあるか確認します。 |
| `LANGUAGE_SERVER_NOT_FOUND` | 設定した `command` が MCP プロセスの `PATH` から実行できるか確認します。 |
| `LANGUAGE_SERVER_START_FAILED` | LSP の起動引数、プロジェクト設定、標準入出力モードを確認します。 |
| `METHOD_NOT_SUPPORTED` | 利用中の LSP サーバーがその LSP 機能を実装していません。別の対応サーバーを使うか、利用可能なツールを使います。 |
| `REQUEST_TIMEOUT` | `--request-timeout` を増やすか、LSP サーバーの起動・プロジェクト解析の問題を確認します。 |
| `STALE_SYMBOL` | 直前に取得したシンボルのファイルが変わっています。シンボルを再検索して新しい `symbol_id` を使います。 |

## 開発・検証

ユニットテストとビルド:

```sh
go test ./...
go build ./cmd/simple-lsp-mcp
```

### Codex 経由のエンドツーエンド検証

`scripts/test-codex-exec.sh` は、Codex CLI、MCP、実際の LSP サーバーを通した検証です。Python、TypeScript、Go、HTML、CSS のテストデータを対象に、Codex が `simple_lsp` ツールを呼び出して期待したシンボルを得ることを確認します。

```sh
bash scripts/test-codex-exec.sh
```

この検証にはログイン済みの Codex CLI が必要で、API 利用枠を消費することがあります。`codex`、`go`、`pyright-langserver`、`typescript-language-server`、`gopls`、`npx`、`jq` が必要です。HTML/CSS の LSP は未キャッシュの場合 `npx` が取得します。

個別ケースだけを実行するには、次のようにします。

```sh
CODEX_E2E_CASE=typescript bash scripts/test-codex-exec.sh
```
