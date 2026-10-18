# M03 install

The installer is the only writer of a local Multica install. It accepts an approved plan whose `effect` is `none`, records an operation, and lets a watcher finish the steps after the page closes.

`market_v1` stays off unless the operator turns it on. The routes live under `/api/market` and still require a human member.

## What a commit does

`POST /api/market/operations` parses the commit document, loads the listing from the server catalog, and runs the same preflight as the install page. A plan that is not ready writes no operation. The idempotency key is unique per workspace. The same key and the same plan hash drive the existing operation. A different plan hash returns 409 and does not step.

The watcher claims due operations whose lease is empty or expired. Reconcile does the same for one operation. A live lease returns the snapshot and does not step.

## Crash rule

A native id is committed on the step while the step is `armed`, in a transaction that does not insert the agent, skill, MCP server, or binding. The insert uses that id in a later transaction. The applied mark is a third commit.

An ambiguous insert becomes `unknown`. That pass does not insert again. Reconcile looks the id up. A hit is applied. A definitive miss re-arms the step for a later tick. A lookup that is not definitive never inserts.

A name collision is `name_collision`. The installer does not adopt the other row by name. A skill or MCP that already exists is adopted only when its content digest matches the catalog. Otherwise the commit is `stale.mapping` and nothing is overwritten.

## Ready does not start a task

Install success can report `readiness: ready`. The operation still carries a refused task step, `task_create_not_uniquely_retryable`. No row is inserted into `agent_task_queue`. There is no task method on the native writer.

A plan that names a squad is refused as a whole with `squad_create_not_uniquely_retryable`. No agent, skill, or MCP row is inserted. Squad names are not unique, and a created squad can omit its leader, so a retry would not be one squad.

Unique task create and an external install callback are still missing. This slice does not add either one.

## Closed gates

The catalog listing has to carry `policyVersion`, `targetRevision`, `releaseDigest`, and a release state of `published` or `live`. The commit's plan, approval, and `current` facts have to match. A missing policy pin is `stale.policy` and writes no row. Sample listings do not carry those pins.

NocoBase does not create the local instance. The outbox payload is the operation id. Step summaries returned by the API are the key, kind, phase, output id, and reason. Instructions, MCP URLs, and env values are not returned.
