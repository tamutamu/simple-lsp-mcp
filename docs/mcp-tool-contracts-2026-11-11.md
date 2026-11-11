# MCPツール契約一覧（2026-11-11 ローカル出荷候補）

`simple-lsp-mcp 0.11.0-rc.20261111` をCodex app-serverに接続して取得した、**実際に公開された22ツール**の入力スキーマを記録しています。元の20ツールは維持され、リモート最新コード由来の2ツールも含みます。

全22ツールの各引数の型・列挙型・説明・必須制約・安全性アノテーションは [機械可読JSON](mcp-tool-contracts-2026-11-11.json) を参照してください。下表はそのスキーマから自動生成しています。

| ツール | 操作種別 | 必須引数 | 他の公開引数 |
|---|---|---|---|
| `analyze_impact` | 参照のみ | `path`, `line` | `depth`, `max_nodes` |
| `apply_code_action` | 書込可能 | `path`, `line`, `column`, `action_id` | `code_action_kinds`, `end_column`, `end_line`, `language` |
| `find_implementations` | 参照のみ | 条件付きターゲット | `column`, `language`, `limit`, `line`, `path`, `relation_view`, `symbol_id`, `symbol_path` |
| `find_references` | 参照のみ | 条件付きターゲット | `column`, `include_declaration`, `language`, `limit`, `line`, `path`, `relation_view`, `symbol_id`, `symbol_path` |
| `format_document` | 書込可能 | `path` | `apply`, `column`, `end_column`, `end_line`, `insert_final_newline`, `insert_spaces`, `language`, `line`, `tab_size`, `trim_final_newlines`, `trim_trailing_whitespace` |
| `get_code_actions` | 参照のみ | `path`, `line`, `column` | `code_action_kinds`, `end_column`, `end_line`, `language`, `limit` |
| `get_declaration` | 参照のみ | 条件付きターゲット | `column`, `language`, `limit`, `line`, `path`, `relation_view`, `symbol_id`, `symbol_path` |
| `get_definition` | 参照のみ | 条件付きターゲット | `column`, `language`, `limit`, `line`, `path`, `relation_view`, `symbol_id`, `symbol_path` |
| `get_diagnostics` | 参照のみ | 条件付きターゲット | `language`, `limit`, `path`, `severities` |
| `get_document_symbols` | 参照のみ | `path` | `detail`, `language` |
| `get_hover` | 参照のみ | 条件付きターゲット | `column`, `language`, `line`, `path`, `symbol_id`, `symbol_path` |
| `get_incoming_calls` | 参照のみ | 条件付きターゲット | `column`, `language`, `limit`, `line`, `path`, `symbol_id`, `symbol_path` |
| `get_outgoing_calls` | 参照のみ | 条件付きターゲット | `column`, `language`, `limit`, `line`, `path`, `symbol_id`, `symbol_path` |
| `get_subtypes` | 参照のみ | 条件付きターゲット | `column`, `language`, `limit`, `line`, `path`, `symbol_id`, `symbol_path` |
| `get_supertypes` | 参照のみ | 条件付きターゲット | `column`, `language`, `limit`, `line`, `path`, `symbol_id`, `symbol_path` |
| `get_type_definition` | 参照のみ | 条件付きターゲット | `column`, `language`, `limit`, `line`, `path`, `relation_view`, `symbol_id`, `symbol_path` |
| `inspect_symbol` | 参照のみ | 条件付きターゲット | `column`, `language`, `line`, `max_lines`, `path`, `related`, `relation_limit`, `symbol_id`, `symbol_path`, `targets`, `view` |
| `list_workspace_symbols` | 参照のみ | 条件付きターゲット | `cursor`, `detail`, `kinds`, `language`, `limit` |
| `preview_format` | 参照のみ | `path` | `column`, `end_column`, `end_line`, `insert_final_newline`, `insert_spaces`, `language`, `line`, `tab_size`, `trim_final_newlines`, `trim_trailing_whitespace` |
| `preview_rename` | 参照のみ | `new_name` | `column`, `language`, `line`, `path`, `preview_detail`, `symbol_id`, `symbol_path` |
| `rename_symbol` | 書込可能 | `new_name` | `apply`, `column`, `language`, `line`, `path`, `symbol_id`, `symbol_path` |
| `search_symbols` | 参照のみ | `query`, `language` | `detail`, `kinds`, `limit` |

## 共通の仕様

- `path` はワークスペース相対パスです。位置指定は原則1始まりです。
- 対象を指定するツールには `symbol_id`、`symbol_path`、または `path`+`line`+`column` 等の**代替的なターゲット指定**があるため、JSONスキーマの `required=[]` であっても、実行時には対象の有効性が検証されます。必要条件がすべて省略できるという意味ではありません。
- 検索結果は、元のLSPセマンティクスに基づく位置と名前を返します。`source` ビューや `include_source` では元コードも返せます。`limit` と `source_limit` は別の制約で、本文の省略は位置の欠落を意味しません。`meta.complete` / `truncated` / `warnings` を確認します。
- 既定の `--result-format both` はJSONテキストと `structuredContent` を返します。`text` はJSONテキストのみ。`readable` は利用可能なソースやhover等を整形し、未対応形状・部分失敗・エラーでは情報を失わないようJSONテキストへ戻します。
- `preview_rename` と `preview_format` は**参照のみ**です。書込可能な `rename_symbol` / `format_document` / `apply_code_action` の適用範囲と承認状態は別途確認が必要です。`rename_symbol` は `apply=true` の場合にのみ編集します。
- 不正引数やLSP失敗はMCPの `isError=true` と共通の `error` オブジェクト（少なくとも `code`、`message`、該当時 `language` / `method`）で伝えます。エラーは正常な取得結果と区別します。

## 出荷判定上の制約

これは実際の**入力契約インベントリ**であって、22ツールそれぞれの戻り値・エラー分岐すべてに対する個別回帰テストの完了証拠ではありません。後者は別途、各言語の実LSP、空結果、最大件数、異常系、権限・編集失敗で合否を記録する必要があります。CIは変更していません。
