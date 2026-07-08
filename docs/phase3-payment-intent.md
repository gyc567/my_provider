# Phase 3 — Payment Intent Flow (3A: Pay-In Provider)

## Context

This plan implements **Phase 3** of the `my-provider` service against the [t-0-network starter README](https://github.com/t-0-network/provider-sdk/blob/master/go/starter/README.md): the asynchronous **Payment Intent Flow** where an end-user pays a pay-in provider in fiat, and a beneficiary provider receives settlement on the crypto side.

Phase 1 (quote publishing) and Phase 2 (`payment.ProviderService.PayOut` / `UpdatePayment` callbacks) are already in place. Phase 3 adds a second, independent server service for payment-intent lifecycle.

**Why now.** The t-0 README's Phase 3 terms (`Pay-In Provider`, `Beneficiary`, `GetPaymentDetails`, `ConfirmFundsReceived`, `publish_payment_intent_quote`, `PaymentIntentUpdate`) **do not match the actual SDK v0.19.0** (verified directly from `/Users/eric/go/pkg/mod/github.com/t-0-network/provider-sdk-go@v0.19.0/`). The SDK has two distinct service packages and a different method naming. The implementation MUST follow the SDK, not the README's letter — see "Naming reconciliation" below.

**Sub-role chosen: 3A — Pay-In Provider.** Recommended because (a) it parallels the existing Phase 2 `payment.ProviderService` server, (b) requires no new outbound RPC shape we haven't seen, (c) the project has EUR/SEPA already configured. Switching to 3B or implementing both is a small fork later; see §13.

**Intended outcome.** The service can (1) receive `CreatePaymentIntent` from the T-0 Network, return a `PaymentUrl` (placeholder), and persist the intent locally; (2) be triggered via an internal admin HTTP endpoint to call `NetworkService.ConfirmPayment` (the "fiat received" RPC); (3) receive `ConfirmPayout` from the network and persist the final state.

---

## Naming reconciliation — README vs SDK v0.19.0

The README's Phase 3 names don't exist. The actual SDK exposes two packages:

| README term | Actual SDK name | SDK location |
|---|---|---|
| Step 3A.1 "publish payment intent quote" | **No equivalent RPC** — there is no quote-publishing side for the 3A flow. Phase 1's `UpdateQuote` (`payment.NetworkService`) is the only quote publisher and is reused unchanged. | n/a |
| Step 3A.2 `GetPaymentDetails` | `ProviderService.CreatePaymentIntent` (server handler we implement) — returns `[]PaymentMethod{PaymentUrl, PaymentMethod}` | `payment_intent/provider/providerconnect.ProviderServiceHandler.CreatePaymentIntent` |
| Step 3A.3 `ConfirmFundsReceived` | `NetworkService.ConfirmPayment` (client call we make → network) | `payment_intent/provider/providerconnect.NetworkServiceClient.ConfirmPayment` |
| (network's ack of crypto payout) | `ProviderService.ConfirmPayout` (server handler we implement) | `payment_intent/provider/providerconnect.ProviderServiceHandler.ConfirmPayout` |
| (optional on-chain settlement) | `NetworkService.ConfirmSettlement` (client call we make → network) | `payment_intent/provider/providerconnect.NetworkServiceClient.ConfirmSettlement` |

For the 3B side (NOT implemented in this plan): `RecipientService.{ConfirmPayIn,ConfirmPayment,RejectPaymentIntent}` (server) + `NetworkService.{CreatePaymentIntent,GetQuote}` (client).

The implementation MUST use the SDK names. The README is outdated.

---

## Scope

In scope for Phase 3A:

- New server handler `paymentintentconnect.ProviderServiceHandler` (we implement).
- New outbound client `paymentintentconnect.NetworkServiceClient` (we call).
- New in-memory state for payment intents (matches existing `payments sync.Map` pattern).
- Internal admin HTTP endpoint to trigger the outbound `ConfirmPayment` call (MVP substitute for real fiat detection).
- First test files in this repo (state machine + decimal helpers, table-driven, `-race`).
- Updates to `cmd/main.go` to wire the new client + handler into the existing rootMux.
- Documentation: `docs/phase3-payment-intent.md` (operator-facing, mirrors `docs/quote-api.md` style).

Out of scope (explicit deferrals, see §13):

- 3B Recipient implementation.
- Real fiat detection (bank webhook integration).
- Persistence beyond process memory.
- `ConfirmSettlement` RPC (optional).
- Admin endpoint authentication.

---

## Critical SDK patterns to reuse

Reuse exactly as shown; don't invent alternatives.

- `network.NewServiceClient[T any](privateKey, clientFactory, opts...)` — generic, T constrained by factory. See `/Users/eric/go/pkg/mod/github.com/t-0-network/provider-sdk-go@v0.19.0/network/client.go:17`.
- `provider.NewHttpHandler(networkPublicKey, buildHandlers ...BuildHandler) http.Handler` — variadic. Multiple `BuildHandler`s can be registered in one call. See `/Users/eric/go/pkg/mod/github.com/t-0-network/provider-sdk-go@v0.19.0/provider/handler.go:25`.
- `provider.Handler[T any](handlerFactory, impl, options ...) BuildHandler` — generic. See `/Users/eric/go/pkg/mod/github.com/t-0-network/provider-sdk-go@v0.19.0/provider/handler.go:51`.
- `provider.StartServer(handler http.Handler, opts ...ServerOption) (ServerShutdownFn, error)`. See `/Users/eric/go/pkg/mod/github.com/t-0-network/provider-sdk-go@v0.19.0/provider/server.go:164`.
- Connect error helpers: `connect.NewError(connect.CodeInvalidArgument, err)`, `connect.CodeInternal`, `connect.CodeFailedPrecondition`.
- `paymentintentconnect.UnimplementedProviderServiceHandler` — embed if we ever want to stub a method; not needed in 3A since we implement both.

---

## File layout

```
my-provider/
├── cmd/
│   └── main.go                                 (MODIFY — second network client, second BuildHandler, rootMux wiring)
├── internal/
│   ├── handler/
│   │   ├── payment.go                          (UNTOUCHED — Phase 2)
│   │   └── payment_intent.go                   (NEW — PaymentIntentServiceImplementation)
│   ├── payment_intent/
│   │   ├── store.go                            (NEW — sync.Map-backed state + transitions)
│   │   ├── store_test.go                       (NEW — table-driven state machine tests)
│   │   ├── decimal.go                          (NEW — common.Decimal helpers: ToString, FromString, Multiply)
│   │   ├── decimal_test.go                     (NEW — table-driven decimal helper tests)
│   │   └── admin_http.go                       (NEW — POST /api/admin/payment_intent/{id}/confirm)
│   └── publish_quotes.go                       (UNTOUCHED — Phase 1 still runs)
└── docs/
    ├── quote-api.md                            (UNTOUCHED)
    └── phase3-payment-intent.md                (NEW — operator-facing design doc, mirrors quote-api.md style)
```

Prerequisite: `internal/quotes/{state,http}.go` (designed in `docs/quote-api.md`, not yet on disk) — not strictly required for Phase 3A to compile, but the rootMux wiring in `cmd/main.go` is simpler if they exist or are added concurrently.

---

## State model — `internal/payment_intent/store.go`

Process-local in-memory state for each payment intent we created or observed.

```go
package paymentintent

type Status string

const (
    StatusCreated         Status = "CREATED"           // we returned a PaymentUrl
    StatusFundsReceived   Status = "FUNDS_RECEIVED"    // we called ConfirmPayment; network owes us a settlement
    StatusPayoutConfirmed Status = "PAYOUT_CONFIRMED"  // network called ConfirmPayout
    StatusRejected        Status = "REJECTED"          // we called RejectPaymentIntent (deferred) OR network will reject
    StatusSettlementLinked Status = "SETTLEMENT_LINKED" // optional: ConfirmSettlement sent
)

type PaymentIntent struct {
    ID                uint64
    Currency          string
    Amount            common.Decimal          // fiat amount the user must pay
    MerchantId        uint32
    PaymentMethod     common.PaymentMethodType
    PaymentURL        string                  // what we returned in CreatePaymentIntent
    Status            Status
    CreatedAt         time.Time
    FundsReceivedAt   *time.Time              // set when we call ConfirmPayment
    PayoutPaymentID   *uint64                 // set when network calls ConfirmPayout
    PayoutConfirmedAt *time.Time
    SettlementTxHash  string                  // populated if we ever call ConfirmSettlement (deferred)
    RejectReason      string
    LastError         string                  // for diagnostics; never returned over the wire
}

type Store struct {
    mu   sync.RWMutex
    byID map[uint64]*PaymentIntent
}

func NewStore() *Store

// Idempotency path: returns existing record or creates a fresh CREATED one.
// Second return value is true iff a NEW record was created.
func (s *Store) GetOrCreate(id uint64, currency string, amount common.Decimal,
    merchantID uint32, paymentMethod common.PaymentMethodType, paymentURL string,
) (*PaymentIntent, bool /*created*/)

func (s *Store) Get(id uint64) (*PaymentIntent, bool)

func (s *Store) MarkFundsReceived(id uint64, at time.Time) error    // CREATED -> FUNDS_RECEIVED
func (s *Store) MarkPayoutConfirmed(id uint64, paymentID uint64, at time.Time) error // FUNDS_RECEIVED -> PAYOUT_CONFIRMED
func (s *Store) MarkRejected(id uint64, reason string, at time.Time) error          // any non-terminal -> REJECTED
func (s *Store) SetLastError(id uint64, msg string)
```

Custom errors in the same file:

```go
var (
    ErrNotFound          = errors.New("payment intent not found")
    ErrInvalidTransition = errors.New("invalid state transition for payment intent")
)
```

`time.Time` fields are pointers so callers can distinguish "not set" from "zero time".

State machine summary (mirrors the test table):

| From | Event | To |
|---|---|---|
| (none) | `GetOrCreate` | `CREATED` |
| `CREATED` | `MarkFundsReceived` | `FUNDS_RECEIVED` |
| `FUNDS_RECEIVED` | `MarkPayoutConfirmed` | `PAYOUT_CONFIRMED` |
| `CREATED` | `MarkRejected` | `REJECTED` |
| `FUNDS_RECEIVED` | `MarkRejected` | `REJECTED` |
| `PAYOUT_CONFIRMED` | (none — terminal until optional settlement) | n/a |

---

## Handler — `internal/handler/payment_intent.go`

```go
package handler

import (
    "context"
    "errors"
    "fmt"
    "log"
    "time"

    "connectrpc.com/connect"
    "github.com/t-0-network/provider-sdk-go/api/tzero/v1/common"
    paymentintent "github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/provider"
    paymentintentconnect "github.com/t-0-network/provider-sdk-go/api/tzero/v1/payment_intent/provider/providerconnect"

    "my-provider/internal/payment_intent"
)

var _ paymentintentconnect.ProviderServiceHandler = (*PaymentIntentServiceImplementation)(nil)

type PaymentIntentServiceImplementation struct {
    store          *paymentintent.Store
    networkClient  paymentintentconnect.NetworkServiceClient
    paymentBaseURL string
}

func NewPaymentIntentServiceImplementation(
    store *paymentintent.Store,
    networkClient paymentintentconnect.NetworkServiceClient,
    paymentBaseURL string,
) *PaymentIntentServiceImplementation
```

### `CreatePaymentIntent`

Validate `PaymentIntentId != 0`, `Currency != ""`, `Amount != nil`. MVP: always return **one** payment method (SEPA for EUR, matching `.env`). Compute `paymentURL = fmt.Sprintf("%s/pay/%d", s.paymentBaseURL, msg.PaymentIntentId)`. Call `store.GetOrCreate(...)` (idempotency). Log id/currency/amount/merchant/created flag. Return the response with the single `PaymentMethod`.

### `ConfirmPayout`

Call `store.MarkPayoutConfirmed(msg.PaymentIntentId, msg.PaymentId, time.Now())`. If `ErrInvalidTransition`, log idempotent retry and return success (the network marks this RPC `IdempotencyIdempotent`). Otherwise return `&ConfirmPayoutResponse{}`. No outbound call from this handler.

---

## Admin HTTP — `internal/payment_intent/admin_http.go`

```go
func AdminHandler(store *Store, networkClient piconnect.NetworkServiceClient) *http.ServeMux
```

Routes:

- `GET  /admin/payment_intent/{id}`         → return JSON of current state.
- `POST /admin/payment_intent/{id}/confirm` → call `networkClient.ConfirmPayment(ctx, {PaymentIntentId, PaymentMethod})` with the persisted `paymentMethod`. On success: `store.MarkFundsReceived(id, now)`. Return JSON with `id`, `status`, `settlement_amount`, `payout_provider_id`.
- Other methods / paths → 404 / 405.

Errors:
- `id` not parseable → 400.
- intent not found → 404.
- `ConfirmPayment` network call fails → 502 + `store.SetLastError(id, err.Error())`.

**NOT authenticated in MVP.** Add a `// TODO: gate behind admin ACL in non-sandbox envs` comment in the file. Do NOT delete this comment when the build passes — surface it in the PR description.

Mount point: `/api/admin/` (NOT `/api/` — that prefix is reserved for `internal/quotes`). The full path is therefore `/api/admin/payment_intent/{id}/confirm`.

---

## Decimal helpers — `internal/payment_intent/decimal.go`

```go
func DecimalString(d common.Decimal) string          // "{Unscaled}*{10^Exponent}" formatted as decimal
func DecimalStringPtr(d *common.Decimal) string      // nil-safe
func DecimalMultiply(a, b common.Decimal) *common.Decimal  // MVP — panic on overflow
```

NEVER use `float64` for currency amounts. NEVER use `fmt.Sprintf("%v", d)` on a `*common.Decimal` (prints `&{...}`).

---

## `cmd/main.go` wiring

Five changes (in this order so `go build ./...` stays green):

1. Add `PaymentBaseURL string` to `Config`. Default in code to `"https://example.com/pay"` if env var empty; log a warning.
2. Add `piNetworkClient, err := network.NewServiceClient(config.ProviderPrivateKey, piconnect.NewNetworkServiceClient, network.WithBaseURL(config.TZeroEndpoint))` after the legacy `networkClient`.
3. Construct `piStore := paymentintent.NewStore()` and `piHandler := handler.NewPaymentIntentServiceImplementation(piStore, piNetworkClient, config.PaymentBaseURL)`.
4. Add a second `BuildHandler` to `provider.NewHttpHandler(...)`:
   ```go
   provider.Handler(paymentintentconnect.NewProviderServiceHandler,
       paymentintentconnect.ProviderServiceHandler(piHandler))
   ```
5. Replace the direct `provider.StartServer(providerServiceHandler, ...)` with the `rootMux` wrapping pattern from `docs/quote-api.md`:
   ```go
   rootMux := http.NewServeMux()
   rootMux.Handle("/tzero.v1.payment.ProviderService/", sdkHandler)
   rootMux.Handle("/tzero.v1.payment_intent.provider.ProviderService/", sdkHandler)
   rootMux.Handle("/api/", quotes.Handler(qStore))                        // when internal/quotes exists
   rootMux.Handle("/api/admin/", paymentintent.AdminHandler(piStore, piNetworkClient))
   shutdownFunc, _ := provider.StartServer(rootMux, provider.WithAddr(config.ServerAddr))
   ```

Update `.env.example` to add `PAYMENT_BASE_URL=https://example.com/pay`.

The two outbound clients (`networkClient`, `piNetworkClient`) have **independent HTTP transports** under the SDK — that's intentional and safe to use concurrently. They share `BaseURL` + `PrivateKey`.

The `initNetworkClient` helper for the legacy client is unchanged. A small helper for the new client is OK if it stays inline and obvious; refactor when a third sub-role is added.

---

## Tests

This codebase has no tests today. Phase 3A adds the first ones, following `/Users/eric/.claude/rules/golang/testing.md` (table-driven, `-race`, ≥80% coverage on new packages).

### `internal/payment_intent/store_test.go`

White-box (package `paymentintent`):

- `TestStore_GetOrCreate_IsIdempotent` — second call with same id returns same pointer, `created=false`.
- `TestStore_MarkFundsReceived_HappyPath` — CREATED → FUNDS_RECEIVED.
- `TestStore_MarkFundsReceived_AlreadyReceived` — second call returns `ErrInvalidTransition`.
- `TestStore_MarkPayoutConfirmed_HappyPath` — FUNDS_RECEIVED → PAYOUT_CONFIRMED.
- `TestStore_MarkPayoutConfirmed_BeforeFundsReceived` — `ErrInvalidTransition`.
- `TestStore_MarkRejected_FromCreated` and `_FromFundsReceived` — succeed.
- `TestStore_MarkRejected_FromPayoutConfirmed` — `ErrInvalidTransition`.
- `TestStore_ConcurrentGetOrCreate` — 100 goroutines, same id, exactly one `created=true`. (race-detection stress test.)

Each table-driven test uses `t.Parallel()`.

### `internal/payment_intent/decimal_test.go`

- `TestDecimalString` — `0.86`, `1000`, `0.001`, nil-safe `<nil>`.
- `TestDecimalMultiply` — `0.86 × 500 = 430.00`, `1.5 × 2 = 3.0`.

Pure functions; no concurrency concerns.

### Handler tests — defer

Hand-rolling a `paymentintentconnect.NetworkServiceClient` stub (~30 lines) would enable an end-to-end handler test (`CreatePaymentIntent` → admin confirm → `ConfirmPayout` → `MarkPayoutConfirmed`). RECOMMENDED but deferrable to a follow-up PR; Phase 3A can land with sandbox smoke tests instead.

---

## Step-by-step order (project compiles at every step)

1. Create `internal/payment_intent/decimal.go`. Verify: `go build ./...`.
2. Create `internal/payment_intent/decimal_test.go`. Verify: `go test -race ./internal/payment_intent/...`.
3. Create `internal/payment_intent/store.go`. Verify: `go build ./...`.
4. Create `internal/payment_intent/store_test.go`. Verify: `go test -race ./internal/payment_intent/...`.
5. Create `internal/handler/payment_intent.go`. Verify: `go build ./...` (handler exists but `cmd/main.go` doesn't use it yet).
6. Create `internal/payment_intent/admin_http.go`. Verify: `go build ./...`.
7. Modify `cmd/main.go` (5 sub-steps per §"cmd/main.go wiring"). Verify: `go build ./... && go vet ./... && go test -race ./...`.
8. Update `.env.example`. Verify: nothing (template only).
9. Write `docs/phase3-payment-intent.md` (operator-facing design doc).

---

## Verification

### Build + static analysis

```bash
cd /Users/eric/dreame/code/my-provider
go build ./...
go vet ./...
go test -race -count=1 ./...
go test -cover ./internal/payment_intent/...   # expect >=80% on the new package
```

### Sandbox smoke test

```bash
export $(grep -v '^#' .env | xargs)
go run ./cmd
# expect: "✅ Step 1.1: Provider server initialized on :8080"
# expect: "Published quote: EUR/SEPA off-ramp=0.86 on-ramp=0.88" every 5s
```

Ask the t-0 team to trigger a payment intent for our merchant ID. Watch for:

```
CreatePaymentIntent received: id=... currency=EUR amount=... merchant=... created=true
```

Then simulate "fiat received":

```bash
curl -X POST http://localhost:8080/api/admin/payment_intent/<id>/confirm
# expect: {"id":..., "status":"FUNDS_RECEIVED", "settlement_amount":"...", "payout_provider_id":...}
```

Inspect state:

```bash
curl http://localhost:8080/api/admin/payment_intent/<id>
# expect: JSON with status=FUNDS_RECEIVED, funds_received_at set
```

When the network completes the crypto payout, expect:

```
ConfirmPayout received: intent_id=... payment_id=... status=PAYOUT_CONFIRMED
```

### Sanity checks (pitfalls)

- **Idempotency**: re-trigger admin endpoint on the same id. Expect 200 with the same `payout_provider_id` (state machine logs "already-received" but doesn't error).
- **Bad path**: `curl .../payment_intent/notanumber/confirm` → 400.
- **Unknown id**: `curl -X POST .../payment_intent/999999/confirm` → 404.
- **Wrong action**: `curl -X POST .../payment_intent/1/foobar` → 404.
- **Race**: bash one-liner with 10 parallel curls; `-race` clean, single terminal state.

---

## Pitfalls to avoid

1. **Don't invent `GetPaymentDetails` / `ConfirmFundsReceived` names.** Use `CreatePaymentIntent` and `ConfirmPayment` from the SDK. Call this out in the PR description.
2. **There is no quote-publishing RPC for payment intents.** Don't add one. Reuse Phase 1's `PublishQuotes` unchanged.
3. **Preserve the rootMux topology.** Don't type-assert `provider.NewHttpHandler`'s return value. Just register it as `http.Handler` under prefixes in the rootMux.
4. **Use `common.Decimal` for amounts throughout — never `float64`.** Every log line, env var, JSON response goes through `paymentintent.DecimalString` (or similar).
5. **Implement only `paymentintentconnect.ProviderServiceHandler`.** Don't try to make one struct satisfy both `providerconnect.ProviderServiceHandler` AND `recipientconnect.RecipientServiceHandler` — the method signatures differ.
6. **The compile-time interface assertion is mandatory.** The line `var _ paymentintentconnect.ProviderServiceHandler = (*PaymentIntentServiceImplementation)(nil)` mirrors `internal/handler/payment.go:39`.
7. **`PaymentIntentId` is the idempotency key from the network.** Always `GetOrCreate`; never overwrite. The network may retry the same call.
8. **Connect procedures are `IdempotencyIdempotent`.** `CreatePaymentIntent` and `ConfirmPayout` may be retried — treat this as a feature, not a bug.
9. **Phase 2 `payment.ProviderService.PayOut` ≠ Phase 3A `payment_intent/provider.ProviderService.ConfirmPayout`.** Different flows, independent state machines. Don't cross-call between them. `payments sync.Map` (Phase 2) and `paymentintent.Store` (Phase 3A) are separate.
10. **Two separate `NetworkServiceClient` instances.** Legacy `paymentconnect.NetworkServiceClient` and `paymentintentconnect.NetworkServiceClient` are distinct generated types. They share `BaseURL` + `PrivateKey` but each has its own HTTP transport under the hood.
11. **`paymentintentconnect.NetworkServiceHandler` is server-side. `paymentintentconnect.NetworkServiceClient` is client-side.** Only the `Handler` (server) goes into `provider.NewHttpHandler`. The `Client` is used internally by the admin HTTP handler. Don't confuse.
12. **`time.Time` fields on records are `*time.Time` (pointers)** so "not yet set" is distinguishable from "set to zero".
13. **Transition methods accept `at time.Time` as a parameter** (not `time.Now()` inside the body) so tests can inject deterministic times. Same convention as `internal/handler/payment.go`.
14. **Admin endpoint is unauthenticated in MVP.** Add a `// TODO: gate behind admin ACL` comment. Surface it in the PR.
15. **`provider.NewHttpHandler` is variadic, not slice-based.** Don't build a `[]BuildHandler` programmatically; the variadic signature is what enables each handler to be wrapped in its own signature-verification middleware.
16. **`PaymentBaseURL` env var must be set, even to a placeholder.** Default to `https://example.com/pay` if empty, with a startup warning log.

---

## What we are NOT doing (explicit deferrals)

- **3B (Recipient) implementation.** Effort estimate 1–2 h, mostly copy-paste of 3A pattern with `recipientconnect.{NetworkServiceClient,RecipientServiceHandler}`. Files: `internal/handler/recipient.go`, third client + BuildHandler in `cmd/main.go`.
- **Real fiat detection.** Substitute with admin HTTP endpoint. Real impl: subscribe to bank webhook (SEPA Instant via Modulr / Banking Circle) → call `networkClient.ConfirmPayment`.
- **Disk persistence.** Process-local only; restarts reset the map. Acceptable for sandbox. Production: Postgres / Redis.
- **`ConfirmSettlement`** (optional 3A client-side). Defer; revisit when an on-chain settlement watcher exists.
- **Admin endpoint authentication.** Add TODO; resolve in a separate change.
- **Refactoring the two `NetworkServiceClient` constructions into a helper.** Keep inline for readability. Refactor when a third sub-role is added.

---

## Effort estimate

- Decimal helpers: 30 min
- Store + state machine: 1 h
- Tests (store + decimal): 1.5 h
- Handler impl: 1 h
- Admin HTTP: 1 h
- `cmd/main.go` wiring: 30 min
- Operator doc (`docs/phase3-payment-intent.md`): 30 min
- Sandbox smoke test with t-0 team: 30 min coordination + 30 min execution

**Total: ~6 hours focused work + sandbox coordination.**

---

## Critical files for implementation

- `/Users/eric/dreame/code/my-provider/internal/payment_intent/store.go` (NEW)
- `/Users/eric/dreame/code/my-provider/internal/payment_intent/store_test.go` (NEW)
- `/Users/eric/dreame/code/my-provider/internal/payment_intent/decimal.go` (NEW)
- `/Users/eric/dreame/code/my-provider/internal/payment_intent/decimal_test.go` (NEW)
- `/Users/eric/dreame/code/my-provider/internal/payment_intent/admin_http.go` (NEW)
- `/Users/eric/dreame/code/my-provider/internal/handler/payment_intent.go` (NEW)
- `/Users/eric/dreame/code/my-provider/cmd/main.go` (MODIFY)
- `/Users/eric/dreame/code/my-provider/.env.example` (MODIFY — add `PAYMENT_BASE_URL`)
- `/Users/eric/dreame/code/my-provider/docs/phase3-payment-intent.md` (NEW — operator doc)

## Reference files to read before coding

- `/Users/eric/go/pkg/mod/github.com/t-0-network/provider-sdk-go@v0.19.0/api/tzero/v1/payment_intent/provider/providerconnect/provider.connect.go` — exact server/client interfaces.
- `/Users/eric/go/pkg/mod/github.com/t-0-network/provider-sdk-go@v0.19.0/api/tzero/v1/payment_intent/provider/provider.pb.go` — message types.
- `/Users/eric/go/pkg/mod/github.com/t-0-network/provider-sdk-go@v0.19.0/api/tzero/v1/common/payment_method.pb.go` — `PaymentMethodType` enum, `PaymentDetails` oneof.
- `/Users/eric/go/pkg/mod/github.com/t-0-network/provider-sdk-go@v0.19.0/provider/handler.go` — `NewHttpHandler`, `Handler[T]` patterns.
- `/Users/eric/go/pkg/mod/github.com/t-0-network/provider-sdk-go@v0.19.0/network/client.go` — `NewServiceClient` usage.
- `/Users/eric/dreame/code/my-provider/internal/handler/payment.go` — pattern to mirror (compile-time assertion, sync.Map store, async callbacks).
- `/Users/eric/dreame/code/my-provider/cmd/main.go` — current wiring to extend.
- `/Users/eric/dreame/code/my-provider/docs/quote-api.md` — rootMux wrapping pattern.