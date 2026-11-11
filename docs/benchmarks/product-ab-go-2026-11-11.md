# Go開発作業における Codex / Codex + simple-lsp MCP 比較（2026-11-11）

**対象は2種類の課題のみです。製品全体の高速化を証明したものではありません。**

## 方法

- 比較条件は Codex単体と Codex + simple-lsp MCP。Codex v0.167.0、`gpt-5.6-sol`、effort=low。各課題は同じGoソーススナップショット、同じ指示、同じテスト環境から独立した作業コピーを作り、両条件で同じ初期コミットハッシュを照合しています。
- それぞれ3回。開始順は反復ごとに A→B / B→A / A→B と交互に変更しています。
- **指示の性質：条件付き利用ガイド**。クロスファイルの意味的リネームでは、MCPが接続されていれば `preview_rename` によって正確な対象を把握し、安全なら `rename_symbol` を利用します。コンパイルエラーのように根拠が明らかな場合は不要なLSP呼び出しを行いません。指示文はA/Bで同じです。単にMCPを任意接続しただけの「完全自由選択」試験ではありません。
- LSPセッションは事前に無関係なGoファイルを開いてウォームアップし、**ウォームアップ時間を作業時間から除外**しています。通常の長寿命セッションでサーバーを起動し直すコストを毎回課さないためです。各試行の秒数もCSVに別記しました。
- 実際のMCP呼び出しと成功応答はCodex app-serverのイベントログから確認。ソースの差分、 `go test ./...` と `go vet ./...` 等をエージェントの自己申告とは別に採点しました。`turn/completed.status=failed` や空応答は成功に含めません。通信エラー・利用制限による中断は別扱いです。
- 結果CSV：[product-ab-go-2026-11-11.csv](product-ab-go-2026-11-11.csv)。チェック実装：[audit_product_ab.py](audit_product_ab.py)、[test_audit_product_ab.py](test_audit_product_ab.py)。実行器：[run_codex_product_ab.py](run_codex_product_ab.py)、課題定義：[codex_product_ab_cases.py](codex_product_ab_cases.py)。実行時の完全なイベントログはローカルの `.scratch/product-ab-evaluation-deployed-3reps-20261111/` と `.scratch/product-ab-evaluation-go-bug-control-20261111/` に保存しています（秘密情報や環境依存パスを含み得るためリモートへはアップロードしていません）。

## 1. 意味的なクロスファイルリネーム（T43）

`internal/tools/engine.go` で定義した `tools.New` を `NewEngine` に変更し、無関係の `New` を維持したまま全参照・関連テストを更新する課題です。

| 反復 | Codex単体 | MCP併用 | MCP実呼出 |
|---|---:|---:|---:|
| 1 | 157.533秒・合格 | 70.971秒・合格 | 2回 |
| 2 | 137.638秒・合格 | 86.081秒・合格 | 2回 |
| 3 | 101.770秒・合格 | 108.866秒・合格 | 2回 |
| **平均** | **132.314秒** | **88.639秒** | **計6回** |

- **平均作業時間33.0%短縮**（この課題の3回反復に限る）。3回中2回はMCP併用が速く、1回は遅い結果でした。
- 非キャッシュ入力トークン合計はCodex単体 **77,711**、MCP併用 **99,572**（**28.1%増**）。時間短縮とトークン節減は同義ではありません。
- 両条件とも **3/3合格**。MCP側はすべての反復で `preview_rename` と `rename_symbol` を1回ずつ実際に呼び出し、エラーなく完了しました。

### 補助的な1ペアの確認

別途、同じ課題に条件付きガイドを適用した単独の試行では、Codex単体が関連テスト中の旧文字列 `"New"` を更新し忘れ、`TestResolveSymbolPathWithGopls` が3回連続で失敗しました。MCP併用側は関連文字列を修正して3回連続で合格しました。ただし**別の1ペアの探索的観察**であり、上表の事前設定した3回の合格率や短縮率に加算していません。

## 2. Goの明白な型エラー修正（T35）

`internal/tools/engine.go` の型不一致を修正する課題です。

| 反復 | Codex単体 | MCP接続あり | MCP実呼出 |
|---|---:|---:|---:|
| 1 | 53.920秒・合格 | 62.031秒・合格 | 0回 |
| 2 | 61.155秒・合格 | 61.312秒・合格 | 0回 |
| 3 | 60.525秒・合格 | 79.049秒・合格 | 0回 |
| **平均** | **58.533秒** | **67.464秒** | **計0回** |

- 両条件とも **3/3合格**。LSP自体は使用されませんでした。型エラーが直接示す場所と通常のテスト・コード読解で対応できたため、ここでの時間差を **LSPの性能差と解釈しません**。
- 非キャッシュ入力トークン合計はCodex単体 **58,529**、MCP接続あり **66,448**（13.5%増）。サンプル変動とツール公開の影響は切り分けられていません。

## 出荷判断への意味

今回確認できたのは、**適した用途で適切に指示した場合、LSPがコード修正へ実際に使われ、3反復で作業時間が短いケースがある**ことです。一方、不要な用途では使わず、効果は確認できませんでした。

自然選択だけの旧試行ではMCP呼び出しがゼロでした。今回の条件付きガイド付き評価と混同しません。2種類・各3反復は一般化や有意差判定には不十分であり、`docs/benchmarks/significance-gates-2026-11-01.md` の20課題以上・各3反復等の製品全体の受け入れ基準は**満たしていません**。Go以外の実開発課題、他種類のリファクタリング、Claude Code実接続、最終配布物、リリース公開も継続確認が必要です。

## 再実行例

認証済みかつ余計なMCP登録がないCodex専用ホームを作成し、実行ファイルをビルドしてから、未使用の出力先を指定してください。認証ファイルをGitや公開ログにコミットしないでください。

```sh
export PRODUCT_AB_CODEX_HOME=/path/to/authenticated-isolated-codex-home
export PRODUCT_AB_MCP=/path/to/built/simple-lsp-mcp
export PRODUCT_AB_OUTPUT=/path/to/fresh-ab-output
python3 docs/benchmarks/run_codex_product_ab.py \
  --cases T35,T43 --repetitions 3 --timeout 330
python3 docs/benchmarks/audit_product_ab.py "$PRODUCT_AB_OUTPUT"
```

`PRODUCT_AB_GO` / `PRODUCT_AB_CODEX` / `PRODUCT_AB_NODE` 等で実行バイナリを固定できます。FastAPIのTypeScript/Python課題には固定スナップショットを `PRODUCT_AB_FASTAPI` で指定します。評価中のAPI利用量・料金を事前に確認してください。
