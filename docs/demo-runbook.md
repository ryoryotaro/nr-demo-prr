# PRC with New Relic Autopilot - Demo Runbook

> このRunbookでは、Production Readiness Checkを「PRC」と表記します。Repository内の既存ファイル名やGitHub Workflow名には、実装上の名称として`prr`や`Production Readiness Review`が残っています。

## 1. デモの目的

このデモで見せるのは、アプリケーションが正常に応答することと、Productionで必要な調査能力が備わっていることは別だという点です。checkoutからpaymentへの決済処理は成功し、Functional TestもPASSします。それでも、運用に必要なTelemetryが欠けていればPRCはNOT READYと判定し、Pull RequestのGitHub CheckをFAILさせます。

単に「APM Agentやライブラリが入っている」ことは、必要な調査ができる証明にはなりません。このデモでは実際にNew Relicへ送信されたTelemetryを、サービス固有のObservability Contractと照合します。PRCのREADY / NOT READYは決定論的に判定し、AIには決めさせません。

NOT READYになった場合だけAutopilotが現在のRunを追加調査し、不足がなぜ問題なのか、どの調査が困難になるのか、次に何を確認すべきかを説明します。結果はWorkflow AutomationからSlackへ届けます。

> Automation detects the observability gap.  
> Autopilot investigates the operational impact.

## 2. デモ構成

アプリケーションはGo製の2サービスです。

```text
POST /checkout
      ↓
prr-demo-checkout（checkout-service）
      ↓ HTTP
prr-demo-payment（payment-service）
      ↓
HTTP 200 / checkout_completed
```

PRCのルールはRepository直下の`observability-contract.yaml`にあります。

```yaml
service: prr-demo-checkout

required_attributes:
  - tenant.id
  - customer.plan

required_dependencies:
  - prr-demo-payment
```

現在チェックしている要件は次の3つです。

- checkout Transactionに`tenant.id`が存在すること
- checkout Transactionに`customer.plan`が存在すること
- current `demo.run_id`から得たcheckoutの`trace.id`を、`prr-demo-payment`も共有していること

各実行は`DEMO_RUN_ID` / Telemetry上の`demo.run_id`で分離されます。PRCとAutopilotは、別RunのTelemetryを今回の証拠として使用しません。

## 3. 事前準備

### アカウントと画面

- [ ] GitHubへログイン済み
- [ ] New Relicへログイン済み
- [ ] デモ用Slack Workspaceを開ける
- [ ] New Relic Workflow Automationで`prr-autopilot`が利用可能
- [ ] New Relic側のSlack Destinationが利用可能
- [ ] Autopilotとデモ用Memoryが利用可能
- [ ] Pull Requestはforkではなく、同じRepository内のbranchから作成する

### GitHub Actions設定

GitHubの **Settings > Secrets and variables > Actions** で、次の名前が設定済みか確認します。実値は画面共有やログへ表示しません。

Repository Secrets:

- [ ] `NEW_RELIC_LICENSE_KEY`
- [ ] `NEW_RELIC_USER_KEY`
- [ ] `NEW_RELIC_ACCOUNT_ID`

Repository Variables:

- [ ] `SLACK_DESTINATION_ID`
- [ ] `SLACK_CHANNEL`

GitHub Actionsでは`NEW_RELIC_NERDGRAPH_ENDPOINT=https://api.newrelic.com/graphql`をWorkflow内で設定しています。Slack Bot TokenやWebhook URLはRepositoryでは使用しません。

### GitとPull Requestの準備

現在確認できているGit構成は次のとおりです。

```text
main:                           8dd1e82  Phase 7.1まで
demo/prr-pull-request-gate:     6972a08  PR Gate基盤
demo/break-observability:       2774e3e  意図的なObservability regression
```

重要: `Production Readiness Review Gate`はbase branch側にWorkflowが必要です。当日は先に`demo/prr-pull-request-gate`をmainへ反映し、その後`demo/break-observability`からmainへのデモPRを作成してください。

- [ ] `demo/prr-pull-request-gate`がmainへ反映済み
- [ ] `.github/workflows/prr-gate.yml`がmainに存在する
- [ ] `demo/break-observability`からmainへのPRを作成済み、または作成直前
- [ ] デモPRはmergeしない運用を関係者と確認済み

推奨PR title:

```text
Demo: break observability for PRR gate
```

推奨PR description:

```text
This pull request demonstrates that functional success does not guarantee production readiness.

Application behavior remains healthy, but the change removes customer.plan telemetry and checkout-to-payment distributed trace continuity. The deterministic PRR gate is expected to return NOT READY and fail the GitHub Check after starting the Slack and Autopilot workflow.
```

### ローカル確認を行う場合

- [ ] Rancher Desktop / Dockerが起動済み
- [ ] `docker compose version`が成功する
- [ ] Go、Git、`curl`、`jq`が利用可能
- [ ] `.env`を使う場合、`.env.example`に記載された変数名を設定済み
- [ ] `.env`やAPI Keyを画面共有しない

状態確認コマンド:

```sh
git status --short --branch
git log --oneline --decorate --graph --all -10
docker compose version
```

## 4. デモ前の画面準備

ブラウザのタブを次の順で用意します。

1. **GitHub Pull Request: Files changed**
   - `demo/break-observability`からmainへのPR
   - `cmd/checkout/main.go`の差分をすぐ表示できる状態
2. **GitHub Pull Request: Checks**
   - `Production Readiness Review Gate`を開ける状態
3. **GitHub Actionsの該当Run**
   - `Run Production Readiness Review gate` stepを開ける状態
4. **Slackの通知先Channel**
   - NOT READY開始通知とAutopilot Investigationを表示できる状態
5. **New Relic Workflow Automation（予備）**
   - `prr-autopilot`のRun一覧。Slackが遅い場合の確認用
6. **New Relic APM / Distributed tracing（予備）**
   - メインでは細かく見せず、質問があった場合だけ使用

メインの画面遷移は次を優先します。

```text
GitHub PR diff
→ GitHub Check / PRC Result
→ Slackの正式結果
→ SlackのAutopilot Investigation
```

## 5. 正常系の確認

正常コードでは次の結果になります。

- Functional Test: PASS
- `tenant.id`: PASS
- `customer.plan`: PASS
- `prr-demo-payment`: PASS
- PRC: READY
- GitHub Check: PASS
- Slack: READY
- Autopilot: 実行しない

### GitHub Actionsで事前確認する方法

Phase 7の手動Workflowは`.github/workflows/prr-demo.yml`です。

1. GitHubの **Actions** を開く
2. **Production Readiness Review Demo**を選ぶ
3. **Run workflow**を押す
4. Gate基盤または正常コードのbranchを選ぶ
5. `mode: complete`を選ぶ
6. Run完了後、PRCがREADY、SlackがREADYであることを確認する

このWorkflowは手動デモ用です。`mode: regression`のNOT READYは意図したデモ結果なので、Job自体はSUCCESSになります。

### ローカルで事前確認する方法

正常なGate基盤branchで実行します。これは実New Relic環境へTelemetryとChange Trackingを送り、Workflow AutomationとSlackも起動します。

```sh
git switch demo/prr-pull-request-gate
./scripts/run-prr-gate.sh
docker compose down -v
```

最後に次が表示されれば正常です。

```text
RESULT: READY
[PASS] Workflow Automation started
[PASS] Pull Request PRR Gate: READY
```

15分の本番デモでは、正常系は事前実行結果を説明するだけでも構いません。メイン時間はObservability Regressionへ使います。

## 6. メインデモ: Observabilityを壊したPull Request

### このPRで壊しているもの

`demo/break-observability`は`demo/prr-pull-request-gate`の直後に作成されたbranchです。差分は`cmd/checkout/main.go`の6行削除だけです。

1. `newrelic.NewRoundTripper(nil)`を使用する条件分岐を削除
   - checkoutのHTTP Clientは`http.DefaultTransport`を使用する
   - paymentへのHTTP通信そのものは継続する
   - checkoutからpaymentへのDistributed Trace continuityは失われる
2. `txn.AddAttribute("customer.plan", request.CustomerPlan)`を削除
   - requestの`customerPlan`はvalidationとpayment requestで引き続き利用する
   - アプリケーション機能は変えず、Telemetryへの記録だけがなくなる

残っているもの:

- `tenant.id`のTransaction attribute
- `demo.run_id`のTransaction attribute
- checkoutからpaymentへのHTTP request
- payment-serviceのHTTP 200
- checkoutの`checkout_completed` response
- 各サービスのNew Relic APM Transaction

### デモ時の操作

1. **Pull Requestの概要を開く**
   - 画面上で見る: sourceが`demo/break-observability`、targetが`main`
   - 話す: 「通常のPRにPRCを組み込んでいます。CIが人工的なregression modeを指定しているわけではありません」

2. **Files changedでHTTP transportの差分を見せる**
   - 画面上で見る: `newrelic.NewRoundTripper(nil)`を使う3行の削除
   - 話す: 「HTTP Client自体は残っています。paymentへの通信は成功しますが、New RelicのTrace contextをつなぐ計装が外れています」

3. **`customer.plan`の差分を見せる**
   - 画面上で見る: `txn.AddAttribute("customer.plan", request.CustomerPlan)`を含む3行の削除
   - 話す: 「customerPlanの機能データは引き続き処理されます。Telemetryへの記録だけがなくなっています」

4. **GitHub Checkへ移動する**
   - 画面上で見る: `Production Readiness Review Gate`
   - 話す: 「PR head SHAのコードをcheckoutし、そのcommitをChange Trackingにも記録しています」

5. **Functional Test結果を見せる**
   - 画面上で見る: `Run Production Readiness Review gate`内のSmoke Testと`Functional Test: PASS`
   - 話す: 「checkoutはHTTP 200、paymentも成功しています。機能テストは通っています」

6. **PRC結果を見せる**
   - 画面上で見る:

     ```text
     [PASS] tenant.id
     [FAIL] customer.plan
     [FAIL] prr-demo-payment
     RESULT: NOT READY
     ```

   - 話す: 「実際に生成されたcurrent RunのTelemetryをContractと照合しています」

7. **Run IDとauthoritative evidenceを見せる**
   - 画面上で見る: `demoRunId`と`readinessEvidence`
   - 話す: 「過去の正常Telemetryが混ざらないよう、今回のRun IDで分離しています」

8. **Workflow Automationが開始されたことを見せる**
   - 画面上で見る: `[PASS] Workflow Automation started`とWorkflow Run ID
   - 話す: 「GitHub CheckをFAILさせる前に、開発チームへの通知と追加調査を開始します」

9. **GitHub Check FAILUREを見せる**
   - 画面上で見る: `[FAIL] Pull Request PRR Gate: NOT READY`と赤いCheck
   - 話す: 「Functional TestはPASSですが、Production ReadyではないためMerge GateはFAILします」

10. **SlackのNOT READY開始通知へ切り替える**
    - 画面上で見る: service、Run ID、failed checks
    - 話す: 「このREADY / NOT READYはAIではなく、Observability ContractとPRCが決定しています」

11. **SlackのAutopilot Investigationを見る**
    - 画面上で見る: 同じRun IDを対象にした追加調査
    - 話す: 「Autopilotはfailed checksだけを調査します。別RunのTelemetryで判定を覆しません」

12. **運用上の意味を説明する**
    - 画面上で見る: customer tierへの影響分析、checkout/payment間の障害切り分け、次の確認ポイント
    - 話す: 「Memoryは、Enterprise顧客の優先度やpayment依存関係など、組織固有の意味付けに利用します。新しいPRCルールを追加するためには使いません」

13. **役割分担をまとめる**
    - 話す: 「PRCは判定、Autopilotは調査、Slackは開発チームへのフィードバック先です」

## 7. 期待する結果

| 項目 | 正常 | Observability Regression |
|---|---|---|
| checkout → payment HTTP | 成功 | 成功 |
| Functional Test | PASS | PASS |
| `tenant.id` | PASS | PASS |
| `customer.plan` | PASS | FAIL |
| checkout → payment Trace continuity | PASS | FAIL |
| PRC | READY | NOT READY |
| Workflow Automation | STARTED | STARTED |
| GitHub Check | PASS | FAIL |
| Autopilot | 実行しない | current Runのfailed checksを調査 |
| Slack | READY | NOT READY開始通知 + Investigation |

## 8. デモ中に強調するポイント

- 「機能テストは通っています。でもProduction Readyではありません。」
- 「APM Agentが入っているかだけなら、コードやライブラリを見れば確認できます。」
- 「今回確認しているのは、実際のTelemetryに運用で必要な情報が入っているかどうかです。」
- 「tenant ID、customer plan、サービス間Traceは、このサービスをProductionで調査するための要件です。」
- 「サービスごとの要件はアプリケーションコードではなく、Observability Contractとして宣言しています。」
- 「PRCのREADY / NOT READYはAIに決めさせていません。」
- 「AIは、機械的なFAILを人間が理解できる運用上のコンテキストに変換するために使います。」
- 「Functional healthとObservability readinessは別です。HTTP成功はTrace continuityの証拠にはなりません。」
- 「Autopilotは今回のRun IDに限定して調査するため、直前の正常Runを混ぜません。」
- 「Platform Engineeringチームは共通の仕組みを提供し、各サービスは自分たちに必要なObservability要件を定義できます。」

## 9. トラブル時のリカバリ

### GitHub Actionsが起動しない

確認順:

1. PRのtargetが`main`か
2. PRが`opened`、`synchronize`、`reopened`のいずれかで起動したか
3. mainに`.github/workflows/prr-gate.yml`が存在するか
4. same-repository branchからのPRか
5. **Actions**画面でWorkflowが無効化されていないか

補足: 現在のローカルGit履歴ではPR Gate基盤は`demo/prr-pull-request-gate`にあります。これをmainへ反映する前にbreak branchのPRを作っても、base側にGate Workflowがないため期待どおり起動しません。

### 必須設定不足で失敗する

`Verify prerequisites and pull request commit` stepを確認します。次の名前だけを照合し、値はログへ出しません。

```text
NEW_RELIC_LICENSE_KEY
NEW_RELIC_USER_KEY
NEW_RELIC_ACCOUNT_ID
SLACK_DESTINATION_ID
SLACK_CHANNEL
```

### Telemetry ingestが間に合わない

`run-demo.sh`は初回10秒待機後、既定で5秒間隔・最大25回PRCを再試行します。

確認する箇所:

- GitHub Actionsの`Waiting ... for New Relic ingest...`
- `Run ID`
- 最終的な`RESULT`
- Readiness APIのERRORか、Telemetry不足によるFAILか

一時的なingest遅延が疑われる場合は、GitHub Actionsの **Re-run failed jobs** で同じPR headを再実行します。modeから結果を決めたり、Contractを変更したりしません。

### Workflow Automationが開始できない

Actionsログで次を確認します。

- `[PASS] Workflow Automation started`があるか
- Workflow Run IDが表示されたか
- `Workflow Automation Start API`のERRORがないか

New RelicではWorkflow Automationの`prr-autopilot`を開き、該当Run IDを確認します。Workflow Startが失敗した場合はPRCシステムのERRORであり、通常のNOT READYとは区別します。

### Slackが届かない

確認順:

1. Workflow Automation Runが開始・完了しているか
2. `SLACK_DESTINATION_ID`と`SLACK_CHANNEL`がRepository Variablesにあるか
3. New RelicのSlack DestinationがConnectedか
4. Destinationが対象Channelへ投稿できるか

Slack TokenやWebhookをRepositoryへ追加して回避しません。Slack通知の主体はWorkflow Automationです。

### Autopilotが時間内に返ってこない

NOT READY開始通知が届いていれば、正式なPRC結果はすでに確定・通知されています。Autopilotは非同期の追加調査で、GitHub Actionsは完了をpollしません。

対応:

- 先にGitHubのNOT READYとfailed checksを説明する
- New Relic Workflow Automationで`runAutopilot`の状態を見る
- 時間がなければ「追加調査は非同期」と説明し、事前に取得したSlack画面へ切り替える

### PRCが想定外の結果になる

確認順:

1. 表示された`Run ID`が今回のものか
2. 実行branch / PR head SHAが想定どおりか
3. `Files changed`に2つの削除が含まれるか
4. Functional Testの成否
5. `[PASS]` / `[FAIL]`の内訳
6. `readinessEvidence`が同じRun IDを指しているか

ローカルで切り分ける場合:

```sh
./scripts/smoke-test.sh
DEMO_RUN_ID=<画面に表示されたRun ID> ./scripts/check-readiness.sh
docker compose logs --no-color --tail=100
docker compose down -v
```

`check-readiness.sh`には`.env`または必要なNew Relic環境変数が必要です。API Keyをコマンド履歴や画面へ直接書かないでください。

## 10. デモ終了後

mergeしてよいもの:

- `demo/prr-pull-request-gate`
  - PR Gate基盤だけを追加するbranch
  - 正常コードではPRC READYになる

mergeしてはいけないもの:

- `demo/break-observability`
  - `customer.plan`とTrace continuityを意図的に壊したPhase 8デモbranch
- `demo/observability-regression`
  - 過去PhaseのObservability regressionデモbranch

デモ終了後の作業:

- [ ] `demo/break-observability`のPRをcloseし、mergeしない
- [ ] Required Status Checkの設定を残すか、デモ環境の運用方針に合わせて確認する
- [ ] ローカルで起動した場合は`docker compose down -v`を実行する
- [ ] `.env`、New Relic key、Slack credentialがcommitされていないことを確認する
- [ ] デモ用Slack通知やNew Relic dataを残す期間を確認する

## 11. 15分デモ用ショート版

- [ ] 「Functional Test PASS ≠ Production Ready」を最初に伝える
- [ ] checkout → paymentの2サービス構成を説明する
- [ ] Contractの3要件: `tenant.id`、`customer.plan`、Trace continuity
- [ ] GitHub PR `demo/break-observability` → `main`を開く
- [ ] `customer.plan`計装削除のdiffを見せる
- [ ] New Relic instrumented HTTP transport削除のdiffを見せる
- [ ] 「HTTP機能そのものは壊していない」と説明する
- [ ] GitHub ActionsでFunctional Test PASSを見せる
- [ ] PRCで`tenant.id` PASSを見せる
- [ ] PRCで`customer.plan` / `prr-demo-payment` FAILを見せる
- [ ] PRC NOT READYとGitHub Check FAILUREを見せる
- [ ] Slackの決定論的なNOT READY通知を見せる
- [ ] 同じRun IDのAutopilot Investigationを見せる
- [ ] customer tier分析とサービス間調査への影響を説明する
- [ ] 「PRCは判定、Autopilotは調査、Slackはフィードバック先」で締める
- [ ] デモPRはmergeしない
