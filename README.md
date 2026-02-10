# Temporal Code Reviewer - Multi-Agent PR Review System

A demonstration of true multi-agent orchestration using Temporal workflows in Golang. This system showcases the difference between sequential skills (human-orchestrated) and autonomous multi-agent systems.

## Features

- **4 Parallel Review Agents**: Security, Style, Logic, and Documentation agents run simultaneously
- **1 Synthesis Agent**: Aggregates results after parallel agents complete
- **Real-time Dashboard**: Visualizes agent progress using Server-Sent Events (SSE)
- **GitHub Webhook Integration**: Autonomous workflow triggering
- **Temporal Orchestration**: No human intervention required

## Architecture

```
GitHub Webhook → Service → Temporal Workflow
                     ↓
                Event Bus (in-memory)
                     ↓
                Dashboard (SSE) ← Browser
```

**Components:**
- **Temporal Workflow** - Orchestrates parallel agents
- **Activity Agents** - Security, Style, Logic, Docs, Synthesis
- **Dashboard** - SSE-based real-time UI
- **Webhook Handler** - Receives PR events, starts workflows
- **Event Bus** - In-memory pub/sub for progress updates

## Quick Start

### Prerequisites

- Docker & Docker Compose
- Go 1.22+

### 1. Start the Services

```bash
docker-compose up
```

Wait ~30 seconds for Temporal to initialize. You should see:
- Temporal UI: http://localhost:8080
- Dashboard: http://localhost:8081
- Webhook: http://localhost:8082

### 2. Trigger a PR Review

Using the demo script:

```bash
./trigger-demo.sh
```

Or manually:

```bash
curl -X POST http://localhost:8082/webhook/pr \
  -H "Content-Type: application/json" \
  -d '{
    "action": "opened",
    "number": 123,
    "repository": {
      "owner": {"login": "example"},
      "name": "test-repo"
    },
    "pull_request": {
      "number": 123,
      "title": "Test PR",
      "diff_url": "https://github.com/example/test-repo/pull/123.diff"
    }
  }'
```

### 3. Watch the Dashboard

The response includes a `dashboard_url`. Open it to watch:
- 4 agents turn blue simultaneously (parallel execution)
- Progress bars animate 0→100%
- Agents turn green upon completion
- Synthesis agent starts after others complete
- Total time: ~15-20 seconds

### 4. Verify in Temporal UI

Open http://localhost:8080 and navigate to the workflow to see:
- Activity timeline showing parallel execution
- 4 activities overlapping in time graph

## Project Structure

```
temporal-code-reviewer/
├── activities/           # Agent implementations
│   ├── security_agent.go
│   ├── style_agent.go
│   ├── logic_agent.go
│   ├── docs_agent.go
│   └── synthesis_agent.go
├── dashboard/           # Real-time UI
│   ├── server.go
│   ├── templates/
│   │   └── index.html
│   └── static/
│       ├── app.js
│       └── style.css
├── events/              # Event bus
│   └── bus.go
├── types/               # Shared types
│   └── types.go
├── webhook/             # GitHub integration
│   └── handler.go
├── workflows/           # Temporal orchestration
│   └── pr_review.go
├── main.go              # Service entry point
├── docker-compose.yml   # Infrastructure
├── Dockerfile           # Service container
└── trigger-demo.sh      # Demo trigger script
```

## How It Works

### Workflow Execution

1. **Webhook Trigger**: GitHub sends PR event to `/webhook/pr`
2. **Workflow Start**: the service starts a Temporal workflow
3. **Parallel Agents**: 4 review agents execute simultaneously
   - Security (5s): Checks for vulnerabilities
   - Style (5-7s): Reviews code formatting
   - Logic (8-10s): Validates correctness
   - Documentation (6-8s): Checks docs
4. **Synthesis**: Aggregates all results (3-5s)
5. **Complete**: Final review summary generated

### Event Flow

- Activities publish events to the event bus
- Dashboard subscribes via SSE
- Real-time updates stream to browser
- Progress tracked with heartbeats

## Development

### Build Locally

```bash
go build -o temporal-code-reviewer .
```

### Run Without Docker

1. Start Temporal (requires separate setup)
2. Set environment variable:
   ```bash
   export TEMPORAL_ADDRESS=localhost:7233
   ```
3. Run the service:
   ```bash
   ./temporal-code-reviewer
   ```

### Run Tests

```bash
go test ./...
```

## Temporal Best Practices

This implementation follows Temporal workflow determinism rules:

- ✅ Uses `workflow.Now(ctx)` instead of `time.Now()`
- ✅ All external calls are activities
- ✅ Activities record heartbeats for long operations
- ✅ Proper timeout and retry configurations
- ✅ Event-driven progress tracking

## Success Criteria

- [x] Docker Compose starts full stack
- [x] Dashboard loads at http://localhost:8081
- [x] Webhook triggers workflow successfully
- [x] Dashboard shows 4 agents running in parallel
- [x] Progress bars update smoothly in real-time
- [x] Synthesis agent starts only after all 4 complete
- [x] Temporal UI shows parallel activity execution
- [x] Total execution time: 15-20 seconds

## Demo Script

```bash
# Terminal 1: Start services
docker-compose up

# Terminal 2: Trigger review
./trigger-demo.sh

# Browser: Open dashboard URL (from trigger response)
# Watch: Parallel agents → synthesis → complete

# Temporal UI: http://localhost:8080
# Verify: Activity timeline shows parallelism
```

## Customization

### Adjust Agent Timing

Edit the sleep durations in `activities/*_agent.go`:

```go
time.Sleep(1 * time.Second) // Adjust per agent
```

### Add New Agents

1. Create `activities/my_agent.go`
2. Register in `main.go`
3. Add to workflow parallel execution
4. Update dashboard UI

### Modify Dashboard

- UI: `dashboard/templates/index.html`
- Styling: `dashboard/static/style.css`
- Logic: `dashboard/static/app.js`

## Troubleshooting

**Temporal won't start:**
- Wait 30 seconds for initialization
- Check PostgreSQL is running: `docker ps`
- Check logs: `docker-compose logs temporal`

**Dashboard not updating:**
- Check browser console for SSE errors
- Verify workflow ID in URL matches running workflow
- Check event bus is receiving events

**Activities not executing:**
- Verify worker is registered: check logs
- Ensure activity names match workflow calls
- Check Temporal UI for activity failures

## License

MIT

## Credits

Built with:
- [Temporal](https://temporal.io) - Workflow orchestration
- [Go](https://golang.org) - Backend language
- [Server-Sent Events](https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events) - Real-time updates
