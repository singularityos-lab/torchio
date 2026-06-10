# Torchio sopra go-foundation

Torchio non reinventa l'infrastruttura applicativa: la prende da
[go-foundation](https://github.com/mirkobrombin/go-foundation), la "missing
standard library" zero-dipendenze dell'ecosistema Singularity. Questo documento
mappa ogni parte di Torchio sul pacchetto di go-foundation che la realizza, cosi'
da non scrivere codice che la lib gia' offre.

Consumo (vedi `go.mod`): il modulo pubblicato, pinnato.

```
require github.com/mirkobrombin/go-foundation v1.1.1
```

(v1.1.1 include un fix alla FSM trovato costruendo Torchio: la macchina ora si
aggancia al campo `State` anche quando non e' il primo campo della struct.)

## Mappatura componente -> pacchetto

| Componente Torchio | Pacchetto go-foundation | Come |
|---|---|---|
| Ciclo di vita di `torchiod` | `pkg/hosting` | `hosting.NewBuilder().ConfigureServices(...).AddService(queueSupervisor).OnStart(...).WithShutdownTimeout(...).Build()`, poi `h.Run(ctx)` |
| Wiring dei servizi (Queue, Transform, Transport, Discovery, Store) | `pkg/di` | `di.Register[*TransformEngine](b, factory, di.Singleton)`, risolti con `di.ResolveType[T](c)` |
| FSM dei job (stati 1:1 con IPP `job-state`) | `pkg/fsm/machine` | tag sul campo `State string`: `fsm:"initial:created; created->validating; validating->pending; pending->transforming; transforming->spooled; spooled->sending; sending->completed; *->canceled"`, con hook `Can<State>()`/`OnEnter<State>()` |
| Event log dei job e stream live verso desktop/GUI | `pkg/events` | `events.Subscribe[JobTransition](bus, h)`, `events.Emit(ctx, bus, JobTransition{...})`; lo stream gRPC e' un subscriber |
| Config TOML piu' override da env | `pkg/configuration` (+ `source/env`, `source/file`) | `NewBuilder().Add(file.New(path)).Add(env.New("TORCHIO_")).Build(ctx)`, poi `cfg.Bind(&Config{})` con tag `conf:"..."` |
| Worker per il rendering WASM per-pagina | `pkg/worker` | `worker.NewPool(n).Submit(func(ctx) error { ... })`, una pagina per task |
| Pool di istanze dei moduli wazero | `pkg/pooling` | `pooling.New[*wasmInstance](factory, pooling.WithMaxSize(n), pooling.WithFinalizer(close))` |
| Logging strutturato (journal/syslog) | `pkg/logger` | `logger.New(logger.WithLevel(...), logger.WithSink(...))` |
| Tracing per-job (validazione -> transform -> invio) | `pkg/telemetry` | `tracer.Start(ctx, "job.transform")`, `meter.Histogram("hop.duration")` |
| Invio IPP idempotente verso la stampante | `pkg/resiliency` | `resiliency.Retry(ctx, send, WithAttempts, WithJitter)`, `NewCircuitBreaker(...)` per printer offline, `NewBulkhead(...)` per limitare la concorrenza |
| `torchio doctor` | `pkg/health` | `reg.Register("ipp-reachability", checker)`, `reg.CheckAll(ctx)` |
| Pipeline di transform multi-hop con rollback | `pkg/saga` | compensazione LIFO se un hop fallisce a meta' |
| TOFU dei certificati, credenziali di share | `pkg/secrets` | store cipher/env per il pinning |
| Tabella effimera `devices_seen` della discovery | `pkg/safemap` + `pkg/collections` | sharded map con TTL, Set/OrderedSet |
| Manifest JSON dei driver, ticket dei job | `pkg/serializer` | policy di serializzazione coerente |
| Panic traces leggibili nel binario | `pkg/errutil` | `defer errutil.Auto()` in `main` (gia' fatto in `cmd/torchio/main.go`) |
| Esiti fallibili interni | `pkg/result` | `Result[T]` dove l'Either pattern e' piu' chiaro di `(T, error)` |

## Note di adozione

- **Un host, non un main fatto a mano.** `torchiod` (M1) sara' un host
  `hosting`: il Queue supervisor e il Transport sono `BackgroundService`, il
  resto e' registrato nel container `di`. `torchio daemon` lo costruisce e fa
  `Run`.
- **La FSM e' dichiarativa, non un grande switch.** Gli stati IPP vivono nel tag
  `fsm`, le regole di business negli hook `Can<State>`. `m.ToMermaid()` da'
  gratis il diagramma per la documentazione.
- **Niente bus custom.** L'event sourcing leggero dei job e lo stream live verso
  il desktop Vala/GTK sono lo stesso `events.Bus`: il DB e lo stream gRPC sono
  due subscriber dello stesso flusso di `JobTransition`.
- **go-foundation e' Go 1.24, Torchio Go 1.26.** Compatibile. Torchio consuma
  `go-foundation v1.1.1` pinnato dal proxy (niente `replace` locale).
