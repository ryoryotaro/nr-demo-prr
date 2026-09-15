# PRR Demo - Phase 5

Production Readiness Reviewの実験に使う、Go製の小さな2サービスアプリケーションです。Phase 5では、決定論的なObservability Contractの判定後にNew Relic Autopilotが実TelemetryとMemoryを調査し、判定の意味、Productionへの影響、改善案を説明します。GitHub連携、CI/CD、自動Approve／Reject、SLO、Alertなどは含みません。

## アプリケーション構成

```text
Client
  | POST /checkout
  v
prr-demo-checkout / checkout-service (:8080)
  | POST /pay（External Segment + Distributed Trace Context）
  v
prr-demo-payment / payment-service (:8081)
```

- `checkout-service`: `POST /checkout` を受け付け、入力を検証してpayment-serviceへ決済を依頼します。
- `payment-service`: 内部APIの `POST /pay` を受け付け、このデモでは決済成功を返します。

HTTP処理には引き続きGo標準ライブラリの `net/http` を使用しています。データベースはありません。アプリケーションの唯一の外部依存はNew Relic Go Agentです。

## New Relicの設定と起動

`.env.example` をコピーして `.env` を作り、New Relicの接続情報を設定します。

```sh
cp .env.example .env
```

```dotenv
NEW_RELIC_LICENSE_KEY=ここに自分のLicense Keyを設定
NEW_RELIC_USER_KEY=ここに自分のUser Keyを設定
NEW_RELIC_ACCOUNT_ID=TelemetryがあるAccount ID
NEW_RELIC_NERDGRAPH_ENDPOINT=自分のNew RelicリージョンのGraphQL URL
OBSERVABILITY_MODE=complete
```

`.env` は `.gitignore` と `.dockerignore` の対象です。実値をソース、Compose設定、READMEへ直接記録しないでください。

サービスをビルドして起動します。

```sh
docker compose up --build -d
```

Composeは同じLicense Keyを環境変数として両コンテナへ渡し、Application名を次のように分けます。

```text
checkout-service -> prr-demo-checkout
payment-service  -> prr-demo-payment
```

各 `main.go` は起動時に `newrelic.NewApplication(newrelic.ConfigFromEnvironment())` を実行します。したがって `NEW_RELIC_APP_NAME`、`NEW_RELIC_LICENSE_KEY`、Distributed Tracing設定は環境変数から読み込まれます。設定を解釈できずAgentを初期化できない場合は、理由をログに出してプロセスを終了します。通常のNew Relic接続処理はバックグラウンドで行われるため、サービス起動時に接続完了を待ってHTTP受付を遅らせることはしません。

このPhaseの対象をAPM TransactionとDistributed Traceに絞るため、AgentによるApplication LoggingはComposeの環境変数で無効にしています。アプリ自身の標準ログはコンテナログへ引き続き出力されますが、AgentはログTelemetryをNew Relicへ転送しません。

停止する場合は次を実行します。

```sh
docker compose down
```

## Smoke Test

サービスを起動した状態で実行します。

```sh
./scripts/smoke-test.sh
```

HTTP 200、checkout成功ステータス、payment-serviceの決済成功ステータスを確認します。APM Agentを追加してもPhase 1のAPI動作は変わりません。New Relicへ確認用データを増やすには数回実行してください。

## 今回の計装をコードで追う

### ApplicationとWeb Transaction

New RelicのApplicationは、Agentがメトリクス、Transaction、Spanを送る単位です。各サービスの `main()` でAgentを個別に初期化するため、New Relic上でも2つのAPM Entityになります。

Web Transactionは、1件の受信HTTPリクエストを表す計測単位です。両サービスでは `newrelic.WrapHandleFunc` で既存handlerを包んでいます。wrapperがリクエスト開始時にTransactionを作り、終了時に閉じ、request contextへTransactionを格納します。

wrapperにはルートとして `/checkout` と `/pay` を渡し、実際の `ServeMux` にはGo 1.22以降のmethod付きpatternを登録しています。このためNew Relic内部のTransaction名は次のとおりです。

```text
WebTransaction/Go/POST /checkout
WebTransaction/Go/POST /pay
```

UIでは先頭の種別が省略され、`POST /checkout`、`POST /pay` と表示される場合があります。

### SegmentとExternal Segment

Segmentは、Transaction内の処理区間とその所要時間を表します。External Segmentはその一種で、外部HTTPサービスへの呼び出しを表します。

completeモードではcheckout-serviceの `http.Client.Transport` に `newrelic.NewRoundTripper(nil)` を設定します。handler内で作るpayment向けrequestは、計装済み受信requestの `r.Context()` を引き継ぎます。RoundTripperはそこから現在のTransactionを取得し、payment呼び出しをExternal Segmentとして記録すると同時にDistributed Trace Headerを送信します。

payment-service側の `WrapHandleFunc` は受信headerをTransactionへ取り込みます。その結果、checkoutのTransaction、payment向けExternal Segment、paymentのTransactionが同じTrace IDで結ばれます。標準Distributed Tracingは現行Go Agentで既定有効ですが、このデモでは意図が明確になるようComposeでも有効にしています。

### Business Context

checkout handlerがJSONを検証した直後、`newrelic.FromContext(r.Context())` で現在のTransactionを取得します。`tenant.id` と `demo.run_id` は両モードで追加し、`customer.plan` はcompleteモードだけで追加します。

```text
tenant.id     <- request.tenantId
customer.plan <- request.customerPlan（completeのみ）
demo.run_id   <- DEMO_RUN_ID
```

payment Transactionにも同じ `demo.run_id` を追加します。`amount` はTelemetryへ追加していません。

## `POST /checkout` の処理の流れ

1. checkout側wrapperが `POST /checkout` のWeb Transactionを開始します。
2. checkout handlerがJSONと必須項目を検証し、モードに応じた属性をTransactionへ追加します。
3. completeでは計装済みRoundTripperがExternal SegmentとTrace Contextを作ります。incompleteでは通常のHTTP Transportが同じpayment APIを呼びます。
4. payment側wrapperがheaderを受け取り、同じDistributed Traceに属する `POST /pay` のWeb Transactionを開始します。
5. payment-serviceが `success: true` と `status: paid` を返します。
6. checkout-serviceが決済成功を確認し、HTTP 200と `checkout_completed` を返します。

呼び出し例:

```sh
curl -i -X POST http://localhost:8080/checkout \
  -H 'Content-Type: application/json' \
  -d '{"tenantId":"tenant-001","customerPlan":"enterprise","amount":12000}'
```

## New Relic UIでの確認

データ送信からUIへの反映には少し時間がかかる場合があります。Smoke Testを数回実行した後に確認します。

### 1. APM Entity

New Relicの **APM & services** を開き、次の2 Entityを検索します。

```text
prr-demo-checkout
prr-demo-payment
```

### 2. Transaction

それぞれのEntityで **Monitor > Transactions**（UI表記により **Monitoring > Transactions**）を開きます。

- `prr-demo-checkout`: `POST /checkout`
- `prr-demo-payment`: `POST /pay`

NRQLでも確認できます。

```sql
FROM Transaction
SELECT count(*)
WHERE appName IN ('prr-demo-checkout', 'prr-demo-payment')
FACET appName, name
SINCE 30 minutes ago
```

### 3. Custom Attribute

Query builderで次を実行します。属性名にドットがあるためバッククォートで囲みます。

```sql
FROM Transaction
SELECT latest(`tenant.id`), latest(`customer.plan`)
WHERE appName = 'prr-demo-checkout'
  AND name = 'WebTransaction/Go/POST /checkout'
SINCE 30 minutes ago
```

Smoke Testのリクエストなら `tenant-001` と `enterprise` が返ります。root Span側も次で確認できます。

```sql
FROM Span
SELECT latest(`tenant.id`), latest(`customer.plan`)
WHERE appName = 'prr-demo-checkout'
  AND name = 'WebTransaction/Go/POST /checkout'
SINCE 30 minutes ago
```

### 4. Distributed Trace

1. `prr-demo-checkout` のTransactions画面で `POST /checkout` を選択します。
2. Transaction detailsから該当サンプルの **Distributed trace** またはtrace detailsを開きます。
3. Traceのservice/mapまたはspan一覧に `prr-demo-checkout` と `prr-demo-payment` があることを確認します。
4. checkoutの配下にpayment向けExternal Segmentがあり、その先にpaymentの `POST /pay` が接続していることを確認します。

Spanデータが両Entityから届いていることは次でも確認できます。

```sql
FROM Span
SELECT count(*)
WHERE appName IN ('prr-demo-checkout', 'prr-demo-payment')
FACET appName, name
SINCE 30 minutes ago
```

## Observability Contract

Observability Contractは、サービスをProductionで運用するために必須とするTelemetryを、機械判定可能な形で宣言したものです。アプリケーションの「処理内容」ではなく「外から観測できなければならない事実」を表します。

この宣言をアプリケーションコードから分離すると、計装実装を読まなくても運用要件が分かり、実装方式を変更しても同じ要件で検証できます。Phase 3の [`observability-contract.yaml`](observability-contract.yaml) は意図的に次の3項目だけに限定しています。

```yaml
service: prr-demo-checkout

required_attributes:
  - tenant.id
  - customer.plan

required_dependencies:
  - prr-demo-payment
```

これは「checkoutの直近Telemetryに2つのBusiness Contextがあり、paymentまで繋がるDistributed Traceが観測できること」をREADYの条件にします。SLO、Alert、Logs、Error rate、Latency、Readiness ScoreはまだContractに含めません。

## Observability Readiness Check

`scripts/check-readiness.sh` はFunctional Testとは独立したObservability Readiness Testです。

```text
scripts/smoke-test.sh       -> アプリケーションがHTTPとして動くか
scripts/check-readiness.sh  -> 必須TelemetryがNew Relicに存在するか
```

shell scriptは `.env` を環境変数として読み込み、小さなGo CLI `cmd/readiness` を実行します。CLIは限定されたContract YAMLを読み、標準ライブラリのHTTP clientでNerdGraphを呼び、JSON responseを判定します。YAMLやGraphQL用の追加ライブラリは使用していません。

### License KeyとUser Key

- `NEW_RELIC_LICENSE_KEY`: Go APM AgentがTransactionやSpanをNew Relicへ送信するための取り込み用keyです。
- `NEW_RELIC_USER_KEY`: Readiness CLIがNerdGraphを通じてTelemetryを読み取るためのAPI認証keyです。

用途が異なるため、License KeyをNerdGraph認証には使用しません。どちらも `.env` だけに設定し、標準出力やNerdGraph error出力には表示しません。

### NerdGraphとNRQLの実行

NerdGraphはNew RelicのGraphQL APIです。CLIは `NEW_RELIC_NERDGRAPH_ENDPOINT` にPOSTし、User Keyを `API-Key` headerへ設定します。Account IDとNRQLは文字列連結したGraphQLではなくvariablesとして送ります。

概念上のGraphQL queryは次の形です。

```graphql
query ReadinessNRQL($accountId: Int!, $nrql: Nrql!) {
  actor {
    account(id: $accountId) {
      nrql(query: $nrql) { results }
    }
  }
}
```

### Required Attributeに使うNRQL

各属性について次のNRQLを実行します。属性名を `tenant.id`、`customer.plan` に置き換え、現在の `DEMO_RUN_ID` と一致し、属性値がnullでないTransaction eventを数えます。件数が1以上ならPASSです。

```sql
FROM Transaction
SELECT count(*) AS presentCount
WHERE appName = 'prr-demo-checkout'
  AND `demo.run_id` = '<current DEMO_RUN_ID>'
  AND `tenant.id` IS NOT NULL
SINCE 30 minutes AGO
```

```sql
FROM Transaction
SELECT count(*) AS presentCount
WHERE appName = 'prr-demo-checkout'
  AND `demo.run_id` = '<current DEMO_RUN_ID>'
  AND `customer.plan` IS NOT NULL
SINCE 30 minutes AGO
```

`keyset()`によるschema確認ではなく実eventを数えるため、属性名だけが知られていて値がない状態はPASSになりません。

### Required Dependencyに使うNRQL

```sql
FROM Span
SELECT uniques(appName) AS apps
WHERE trace.id IN (
    SELECT uniques(trace.id)
    FROM Span
    WHERE appName = 'prr-demo-checkout'
      AND `demo.run_id` = '<current DEMO_RUN_ID>'
  )
FACET trace.id
SINCE 30 minutes AGO
LIMIT MAX
```

subqueryが今回のrun_idを持つcheckout SpanのTrace IDだけを選び、外側のqueryがTraceごとのApplication一覧を返します。その一覧に `prr-demo-payment` があればPASSです。単にpayment Transactionが存在するだけ、または過去のcomplete traceが存在するだけではPASSになりません。

### PASS / FAIL / ERROR

- `PASS`: NerdGraph queryが正常に完了し、直近30分に要件を満たすTelemetryがありました。
- `FAIL`: queryは正常に完了しましたが、該当Telemetryがありません。最終結果は `NOT READY`、終了コードは1です。
- `ERROR`: 環境変数不足、認証失敗、通信失敗、GraphQL/NRQL error、不正responseなどでチェック自体を完了できません。最終結果は `ERROR`、終了コードは2です。

すべてPASSの場合だけ `RESULT: READY` と終了コード0を返します。API障害をTelemetry欠損として扱わないため、FAILとERRORを分離しています。

### 実行方法

Phase 4では通常、後述の `run-demo.sh` を使用します。Readiness Checkを単体実行する場合は、対象Telemetryと同じrun IDを指定します。

```sh
DEMO_RUN_ID=<対象run ID> ./scripts/check-readiness.sh
```

## complete / incompleteデモ

`OBSERVABILITY_MODE` はcheckout-serviceのTelemetry生成方法だけを切り替えます。

| モード | `tenant.id` | `customer.plan` | checkout → payment Trace Context | HTTP決済 |
|---|---|---|---|---|
| `complete` | 送信 | 送信 | 伝搬 | 成功 |
| `incomplete` | 送信 | 欠損 | 伝搬しない | 成功 |

incompleteでもrequestの `customerPlan` は通常どおり検証・paymentへ送信されます。またpayment-service自身のAPM Transactionと `demo.run_id` は引き続き記録されます。失われるのはcheckoutの `customer.plan` 属性と、checkoutからpaymentへのTrace Contextだけです。

`DEMO_RUN_ID` は各デモ実行を識別する値です。30分以内に残っている別モードのTelemetryを拾わないよう、属性とdependencyの全queryをこの値で限定します。値は `run-demo.sh` が実行ごとに生成し、Compose経由で両サービスへ同じ値を渡します。

Observability ContractはPhase 3から変更していません。モードによって合格ルールを変えるのではなく、実際のTelemetryが同じContractを満たすかどうかを変えています。

### 実行方法

```sh
./scripts/run-demo.sh complete
```

```sh
./scripts/run-demo.sh incomplete
```

scriptはモード検証、run ID生成、Compose再作成、既存Smoke Test、Telemetry ingest待機、Readiness Checkを順に実行します。最初の待機は既定10秒で、反映前なら5秒間隔で最大25回再試行します。2つのAgentのharvest時刻がずれても確認でき、結果が揃えば上限を待たず終了します。Agent接続やharvestのタイミングに依存しないよう、再試行時にも同じSmoke Test requestを1回ずつ送り、表示上のFunctional Test結果とは分けて扱います。必要なら次の環境変数で調整できます。

```text
NEW_RELIC_INGEST_WAIT_SECONDS
NEW_RELIC_INGEST_RETRY_SECONDS
NEW_RELIC_INGEST_ATTEMPTS
```

completeの期待結果:

```text
=== Observability Readiness Check ===

Service: prr-demo-checkout
Run ID: <generated run ID>
Window: last 30 minutes

Required attributes
[PASS] tenant.id
[PASS] customer.plan

Required dependencies
[PASS] prr-demo-payment

RESULT: READY
```

incompleteの期待結果:

```text
Functional Test: PASS

Required attributes
[PASS] tenant.id
[FAIL] customer.plan

Required dependencies
[FAIL] prr-demo-payment

RESULT: NOT READY
EXPECTED RESULT: NOT READY
```

`check-readiness.sh` は従来どおりREADY=0、NOT READY=1、ERROR=2です。一方、`run-demo.sh incomplete` はこの特定のNOT READYを期待されたデモ結果と解釈し、`EXPECTED RESULT: NOT READY` を表示して終了コード0にします。tenantまで欠ける、dependencyがPASSする、API ERRORになる、といった想定外の結果は成功扱いしません。

## AI-assisted Production Readiness Review

Phase 5では [`newrelic/workflows/prr-autopilot.yaml`](newrelic/workflows/prr-autopilot.yaml) を追加します。判定処理には手を加えず、判定後の調査と説明だけをWorkflow AutomationからAutopilotへ依頼します。

```text
Observability Contract
        │
        ▼
 READY / NOT READY
 Deterministic
        │
        ▼
    Autopilot
        │
        ├── Actual Telemetry
        ├── Demo Run ID
        └── Memory
              │
              ▼
      Explanation / Investigation
              │
              ▼
         Recommendation
```

### Contract / Memory / Autopilotの役割

| 要素 | 役割 | 合否を変更できるか |
|---|---|---|
| Observability Contract | Productionに必須な属性とdependencyを宣言する | ルールそのもの |
| `check-readiness.sh` | Contractと実Telemetryを照合しREADY / NOT READYを決定する | 唯一の判定処理 |
| Memory | サービス固有の業務・運用コンテキストを保持する | できない |
| Autopilot | TelemetryとMemoryを調査し、影響と改善案を説明する | できない |

Autopilotは生成AIなので、説明や推奨は調査ごとに表現が変わり得ます。合否まで任せると再現可能性が失われ、Contractにない要件を暗黙に追加する危険があります。そのためworkflow promptは、入力されたContract結果をauthoritativeとして扱い、再判定や上書きをしないよう明示しています。

### Workflow AutomationとRun Autopilot Action

Workflow Automationは、入力、Action、分岐などを公式YAMLで定義し、New Relic内で実行する仕組みです。このデモのworkflowは1つのActionだけを持ちます。

```yaml
action: newrelic.autopilot.run
version: '1'
```

`newrelic.autopilot.run` はpromptと最大10件のcontextを受け取り、Autopilot調査を実行する公式Actionです。このworkflowが渡すcontextは次の5件だけで、secretは含みません。

- `service`: 調査対象APM Application
- `demoRunId`: 対象Telemetryを限定する `demo.run_id`
- `observabilityReadinessResult`: Contractが既に決めたREADY / NOT READY
- `failedChecks`: ContractでFAILした項目
- `accountId`: `${{ .workflowConstants.accountId }}` から取得した実行Account ID

Account IDは手動inputにしません。Workflow Automationが提供する実行時定数を使うため、別Accountへ定義を登録しても、その実行scopeとTelemetry調査先が一致します。License KeyやUser Keyをworkflow input、context、promptへ渡すことはありません。

Actionの `success` と `errorMessage` は予約済み標準出力です。加えてselectorで次を公開します。

```yaml
selectors:
  - name: error
    expression: .errorMessage
  - name: response
    expression: .response.finalAnswer
```

これによりrun detailsで成功状態、エラー、Autopilotの最終回答を確認できます。

### Workflow Input

すべて必須です。

| Input | 型 | incompleteの例 | completeの例 |
|---|---|---|---|
| `service` | String | `prr-demo-checkout` | `prr-demo-checkout` |
| `demoRunId` | String | `run-demo.sh` が表示した値 | `run-demo.sh` が表示した値 |
| `readinessResult` | Enum | `NOT READY` | `READY` |
| `failedChecks` | String | `customer.plan, prr-demo-payment` | `none` |

`readinessResult` と `failedChecks` はAutopilotの推測値ではなく、必ず同じrun IDに対する `check-readiness.sh` の出力を入力します。

### Prompt設計

promptはIncident RCAではなくAI-assisted Production Readiness Reviewであることを宣言し、次の順序を要求します。

1. 今回のrun IDについて確認できたTelemetry上の事実
2. ContractでFAILした項目、またはFAILなし
3. 欠損signalがProductionで重要な理由
4. Memoryから得た業務・運用コンテキスト
5. 欠損により難しくなる調査
6. 推奨する修正

回答には「Verified telemetry facts」「Saved operational context from Memory」「Interpretation」「Recommendation」を区別させます。確認できない事柄は推測で埋めず、確認不能と明示させます。

### AutopilotとMemoryの事前設定

Autopilotを使用するAccountが分析対象として設定され、Workflow Automationから利用できる権限があることを確認します。New Relicの **Ask AI** パネルからAutopilotを開き、必要な業務・運用上の事実を会話で伝え、その回答を **Save to memory** でAccountまたはOrganization scopeへ保存します。Memoryの作成・更新はRepositoryから自動化しません。

デモ用Memoryに保存するコンテキスト例:

- `prr-demo-checkout` ではenterprise顧客の影響をstandard顧客より優先して判断する。`customer.plan` は障害時に顧客Tierごとの影響を切り分けるために使用する。
- `prr-demo-payment` はcheckoutの重要な下流依存サービスである。checkoutからpaymentまでTraceが連続していることは、サービス間障害を切り分けるうえで重要である。

これらは「なぜsignalが重要か」というContextです。「`customer.plan` がなければNOT READY」のような判定RuleはMemoryへ保存しません。またenterprise顧客に関する情報はworkflow promptへハードコードしていません。回答にその情報が現れることで、Memoryが利用されたことをデモできます。

### Workflow YAMLのvalidateと登録

New RelicのNerdGraph API Explorerを開き、workflowを登録するAccountを選びます。ローカルYAMLをbase64へ変換します。

```sh
base64 < newrelic/workflows/prr-autopilot.yaml | tr -d '\n'
```

まず公式 `workflowDefinitionValidation` queryで検証します。`ACCOUNT_ID` と `BASE64_YAML` はExplorerのvariablesへ指定しても構いません。

```graphql
query ValidateWorkflow($accountId: Int!, $yaml: SecureValue!) {
  actor {
    account(id: $accountId) {
      workflowAutomation {
        workflowDefinitionValidation(definition: {yaml: $yaml}) {
          valid
          errors { message }
        }
      }
    }
  }
}
```

```json
{
  "accountId": 1234567,
  "yaml": "BASE64_YAML"
}
```

このRepositoryの定義は実Accountの公式validationで `valid: true`、errorsなしを確認済みです。

validation後、NerdGraph API Explorerで公式create mutationを手動実行します。これは同名定義が既にある場合はエラーになるため、既存定義の変更には公式UpdateWorkflowDefinitionを使用してください。

```graphql
mutation CreatePRRWorkflow($scopeId: String!, $yaml: SecureValue!) {
  workflowAutomationCreateWorkflowDefinition(
    scope: {id: $scopeId}
    definition: {yaml: $yaml}
  ) {
    definition { name }
  }
}
```

```json
{
  "scopeId": "1234567",
  "yaml": "BASE64_YAML"
}
```

この手順は公式API Explorerからの手動登録です。Repositoryにはworkflowを自動作成・更新する独自toolを追加していません。

### incompleteモードのPRRデモ

1. デモを実行します。

   ```sh
   ./scripts/run-demo.sh incomplete
   ```

2. Functional TestがPASSし、`customer.plan` と `prr-demo-payment` がFAIL、`RESULT: NOT READY` になったことを確認し、表示されたRun IDを控えます。
3. New Relicで **All Capabilities > Workflow Automation** を開き、`prr-autopilot` のentity overviewから手動実行します。
4. 次のinputを指定します。

   ```text
   service: prr-demo-checkout
   demoRunId: <Step 2のRun ID>
   readinessResult: NOT READY
   failedChecks: customer.plan, prr-demo-payment
   ```

5. Run historyから `runAutopilot` stepを開き、標準 `success`、`errorMessage` とselectorの `error`、`response` を確認します。

期待する回答はNOT READYの再判定ではありません。今回runで `customer.plan` がないこと、checkoutとpaymentが同じTraceにないこと、それにより顧客Tier別影響分析やサービス境界の切り分けが難しくなること、Memoryにある固有Context、修正案が区別して説明されることを確認します。

### completeモードのPRRデモ

1. `./scripts/run-demo.sh complete` を実行し、Run IDと `RESULT: READY` を確認します。
2. 同じworkflowへ次を入力します。

   ```text
   service: prr-demo-checkout
   demoRunId: <completeのRun ID>
   readinessResult: READY
   failedChecks: none
   ```

3. AutopilotがREADYを再判定せず、今回観測できたBusiness Contextと連続Traceによって、どのようなProduction調査が可能かを簡潔に説明することを確認します。

Workflow実行結果はWorkflow Automationのentity overviewにあるRun historyとstep outputsで確認します。Slack通知や外部送信はありません。

## Phase 6: Change TrackingとGitHub Context

Phase 6では、Contractの判定ロジックを変えず、デプロイに対応するcommitとGitHubへのリンクをNew Relic Change Trackingへ記録します。Autopilotは、そのChange eventとNew Relic側で接続したGitHub PRを、`failedChecks`の説明に必要な範囲だけで利用します。

```text
Git commit / Pull Request
        │
        ▼
Change Tracking Deployment event
        ├── prr-demo-checkout
        ├── version / commit SHA
        ├── demoRunId / observabilityMode
        └── GitHub deep link
                    │
Demo Telemetry ────┤
Memory ────────────┤
                    ▼
             Autopilot explanation

READY / NOT READY = Observability Contract + Readiness Check only
```

### Change Tracking Event

Change Trackingは、デプロイや設定変更などをTelemetryの時間軸と関連付けるNew Relicの仕組みです。このデモはLegacy Deployment Markerではなく、`changeTrackingCreateEvent` NerdGraph mutationを使用し、次のDeployment eventを作成します。

| フィールド | 値 |
|---|---|
| category / type | `Deployment` / `Basic` |
| version | commit SHAの先頭7文字 |
| commit | `git rev-parse HEAD` の完全SHA |
| description / shortDescription | `PRR demo deployment` / `PRR demo` |
| customAttributes | `demoRunId`, `observabilityMode` |
| deepLink | `GITHUB_PR_URL`、空なら`GITHUB_REPOSITORY_URL` |
| user | Gitの`user.name`、未設定なら`prr-demo` |

対象EntityのGUIDは固定しません。`NEW_RELIC_ACCOUNT_ID`を正の整数として検証し、次のentity searchを作ります。

```text
name = 'prr-demo-checkout' AND domain = 'APM' AND accountId = <NEW_RELIC_ACCOUNT_ID>
```

Accountまで絞ることで、同名Entityが別Accountに存在しても対象を一意に解決しやすくします。

commit SHAはChange eventとGitHub履歴を結ぶ識別子です。GitHub Integrationが利用できる場合、Autopilotが対応するPRのtitle、description、changed files、commit情報などを取得する手掛かりになります。ただし、commitとTelemetry欠損が時間的に近いことは因果関係の証明ではありません。Workflow promptは変更情報をVerified change context、原因との関係をInterpretationとして分離します。

### demo.run_idとdemoRunId

同じ値を異なるデータ型の命名規則に合わせて記録します。

- Transaction / Span Telemetry: `demo.run_id`
- Change Tracking Event: `customAttributes.demoRunId`

これによりAutopilotは、今回のデモTelemetryと今回のDeployment eventを同じ実行として探せます。

### 環境変数

`.env`へGitHubの公開URLだけを任意で設定します。

```sh
GITHUB_REPOSITORY_URL=https://github.com/example/nr-demo-prr
GITHUB_PR_URL=https://github.com/example/nr-demo-prr/pull/1
```

`GITHUB_PR_URL`が空ならrepository URLをdeep linkにします。GitHub PATは`.env`や`.env.example`へ保存しません。Change CLIは既存の`NEW_RELIC_USER_KEY`、`NEW_RELIC_ACCOUNT_ID`、`NEW_RELIC_NERDGRAPH_ENDPOINT`だけを使います。

### record-change.sh

Git repositoryとHEAD commitが準備済みなら、次のように単独実行できます。

```sh
DEMO_RUN_ID=20260915-example \
OBSERVABILITY_MODE=incomplete \
./scripts/record-change.sh
```

スクリプトは完全SHAをGitから取得し、[`cmd/change`](cmd/change/main.go)を通してChange eventを送信します。Git repositoryでない場合やcommitがない場合は`[ERROR]`と終了コード2を返します。未commit変更がある場合は、eventがworking treeではなくHEADを指すことを`[WARNING]`で知らせますが、送信は継続します。API keyは出力しません。

### run-change-demo.sh

GitHub / Change Trackingを含む一連のデモは次で実行します。

```sh
./scripts/run-change-demo.sh incomplete
```

このスクリプトはrun IDを一度だけ生成し、Change eventを記録してから、同じ`DEMO_RUN_ID`を外部指定して`run-demo.sh`を実行します。`run-demo.sh`は外部値がない場合だけ従来どおりrun IDを生成するため、既存の`./scripts/run-demo.sh complete|incomplete`も変わらず利用できます。最後にWorkflowへ渡す`service`、`demoRunId`、`readinessResult`、`failedChecks`を表示します。

### ローカルGitとGitHub Repositoryの手動準備

このPhaseの実装では`git init`やcommitを自動実行しません。準備するときは内容を確認したうえで手動で行います。

1. ローカルでGit repositoryを初期化し、complete状態を最初のcommitとして記録する。
2. GitHubでrepositoryを作成し、remoteを設定してmainへpushする。
3. `demo-incomplete` branchを作り、Observabilityに関係する小さな変更をcommitする。
4. GitHubでPull Requestを作成し、内容を確認してmergeする。
5. merge後の完全commit SHAをHEADとしてcheckoutする。
6. `GITHUB_REPOSITORY_URL`と、任意で`GITHUB_PR_URL`を設定する。
7. `./scripts/run-change-demo.sh incomplete`を実行する。

CodexやスクリプトはGitHub repository作成、push、PR作成、mergeを行いません。

### Autopilot GitHub Integrationの手動設定

Repository外の事前条件です。

- 対象AccountでAutopilotが利用可能であること
- Autopilot GitHub Integration Previewが利用可能であること
- New Relic Organization Manager権限があること
- GitHub側で接続に必要な管理権限があること
- 必要最小限のread-only権限を持つfine-grained Personal Access Tokenを準備すること
- Change Tracking eventにGitHub上のcommit SHAが含まれていること

New Relic UIからGitHub Integrationをread-onlyとして接続します。PATはNew Relic側の接続設定だけで扱い、Repository、`.env`、workflow context、Change eventへ入れません。PRのdiffやコードはAutopilotの調査コンテキストになり得るため、hardcoded credential、API key、password、highly sensitive dataを含むRepositoryではデモしないでください。

### Autopilot Workflowの実行と確認

`run-change-demo.sh`が最後に表示した4つの値を、Workflow Automationの`prr-autopilot`へ入力します。Workflow Input、context、selectorはPhase 5から変更していません。

回答は次の5セクションに限定されます。

1. Verified telemetry facts
2. Saved operational context from Memory
3. Verified change context from Change Tracking / GitHub
4. Interpretation
5. Recommendation

3番目で、今回のChange event、commit SHA、関連PR、failed checkに関係するchanged filesなどが確認できればGitHub Contextを取得できています。PR全体のCode Reviewは対象外です。GitHub Contextが得られない場合は`No relevant GitHub change context was available.`と明記し、TelemetryとMemoryによる調査を継続します。

PRに変更が存在することはVerified change contextですが、その変更がTelemetry regressionを起こしたことは、追加証拠がなければInterpretationです。Autopilotには時間的な近接だけで原因を断定させません。READY / NOT READYは引き続きContractとReadiness Checkだけが決めます。

GitHub Integrationの接続、commit SHAからのPR取得、回答へのPR title・description・changed filesの反映はNew Relic UIでの **MANUAL VERIFICATION REQUIRED** です。

## Phase 6.5: Git履歴で再現するObservability Regression

Phase 6.5では、GitHub Pull RequestとAutopilot GitHub Contextのデモ専用に`regression`モードを追加します。3モードの役割は次のとおりです。

| モード | Functional Test | Readiness | 用途 |
|---|---|---|---|
| `complete` | PASS | READY | 正常なTelemetryのbaseline |
| `incomplete` | PASS | NOT READY | Phase 4/5のContractとAutopilot説明用 |
| `regression` | PASS | NOT READY | Phase 6のChange TrackingとGitHub PR Context用 |

`incomplete`と`regression`のTelemetry結果は同じです。ただし`regression`は、正常なmainからObservability実装を壊したPull Requestのcommit SHAをChange Trackingへ記録し、Autopilotがそのコード変更を調査コンテキストとして利用するために存在します。

### regressionで変わるObservability

checkoutの機能処理とpaymentへのHTTPリクエストは変えません。`regression`では次の2点だけを欠損させます。

- `tenant.id`と`demo.run_id`は記録するが、`customer.plan`をcheckout Transactionへ追加しない。
- 通常のHTTP transportでpaymentを呼び出し、New Relicのinstrumented HTTP transportを利用しない。paymentはHTTP 200を返し、自身のAPM Transactionも継続するが、checkoutとのDistributed Trace continuityは失われる。

この変更は`cmd/checkout/main.go`の属性追加条件とHTTP transport選択条件に明示されるため、GitHub PRのdiffから両方を確認できます。

### baselineとregression branch

`main`の`Baseline: working PRR demo through Phase 6` commitは、complete/incomplete、Contract、Readiness Check、Autopilot、Memory、Change Tracking、GitHub Context対応までが動く基準点です。

`demo/observability-regression`の`Demo: introduce observability regression` commitは、そのbaselineにregressionモードだけを追加します。GitHubではこのbranchからmainへのPull Requestを作成します。

### 推奨Pull Request

Title:

```text
Demo: introduce observability regression
```

Description:

```text
This change simulates an observability regression for the Production Readiness Review demo.

Application functionality remains healthy, but:

- customer.plan is no longer recorded in checkout telemetry
- distributed trace continuity between checkout and payment is intentionally broken

The deterministic Observability Contract is expected to return NOT READY while functional tests continue to pass.
```

### GitHubでの手動操作とデモ

1. GitHubで空のrepositoryを作成し、ローカルmainをpushする。
2. `demo/observability-regression`をpushする。
3. `demo/observability-regression`からmainへのPull Requestを、上記titleとdescriptionで作成する。
4. PR diffで`customer.plan`とinstrumented transportの条件変更を確認してmergeする。
5. merge commitをローカルへ取得し、そのcommitをcheckoutする。
6. `./scripts/run-change-demo.sh regression`を実行する。
7. Change Tracking eventのcommit SHAと`demoRunId`を確認する。
8. 表示された入力でAutopilot Workflowを実行する。

Autopilot GitHub IntegrationはChange Trackingのcommit SHAから関連PRを探し、failed checksに関係する変更情報を利用します。ただし、PRとTelemetry regressionが時間的に近いことだけでは因果関係を証明できません。PRのdiffはVerified change contextであり、それが欠損原因であるという説明は、追加の証拠がない限りInterpretationとして扱います。
