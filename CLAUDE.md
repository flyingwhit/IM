# This is an instant messeging backend project written in Go .

Goals:
- Learn Backend Engineering.
- Learn Distributed System disign.
- Learn AI-assist software development.
- Write production style code


## Development Principle

- Don't over-engineer.
- Keep the implementation simple.
- Prefer standard library as possible.
- Small commits.
- Every feature should be testable.
- Explain design trade-offs before make architecture change.
 
## Coding style

- Idiomatic Go.
- Follow Effective Go
- Keep function under 50 lines when possible.
- Avoid unnecessary abstrations.
- Comments should explain WHY, not WHAT.

## AI Collaboration

Before writing code:

- Explain your design.
- Mention alternative solutions.

When modifying code:

- Preserve existing APIs unless necessary.
- Keep diffs as small as possible.
- Never rewrite unrelated files.

After Implementation:

- Explain what changed
- Mention potential improvements.
- Point out edge cases

## Stack

Language: Go

HTTP: Gin

Database: PostGreSQL

Cache: Redis

Message queue: Kafka

Protocol: RestAPI

## Current Milestone

### Phase 1 - Core Backend

- User registration
- Login
- JWT authentication
- PostGreSQL
- Friend system
- Redis cache

### Phase 2 - Realtime Messaging

- WebSocket
- Online status
- Private messaging
- Heartbeat
- Offline message storage

### Phase 3 - Group Chat

- Group management
- Group messaging
- Read receipts
- Message history

### Phase 4 - Scalability

- Multiple Websocket gateways
- Service decomposition
- Kafka event bus
- Message persistence pipeline

### Phase 5 - Observability

- Structured logging
- Prometheus metrics
- Grafana dashboards
- Health checks

### Phase 6 - Production Readiness

- Docker Compose
- CI/CD
- Configuration management
- Graceful shutdown
- Load testing



## Learning Goals

While implementing features:

- Explain Go concurrency patterns.
- Explain why this database schema is designed this way.
- Explain performance considerations.
- Point out common production pitfalls.

## Code Review

Whenever adding code:

- Point out bugs.
- Suggest simplifications.
- Mention performance concerns.
- Mention security concerns.

## Git

One logical change per commit.

Commit message format:

feat:
fix:
refactor:
test:
docs:


## Teaching Mode

Treat me as an engineer who wants to learn.

Do not immediately provide the final implementation.

Instead:

1. Explain the problem.
2. Discuss the design.
3. Propose a plan.
4. Wait for confirmation before writing large amounts of code.
5. Explain non-obvious implementation details.
6. Encourage incremental implementation.


## Documentation Guidelines

Maintain documentation under docs/.

Documentation should explain engineering concepts and design decisions, not duplicate code.

Important documents:

architecture.md:
- System architecture
- Module responsibilities
- Request/data flow
- Evolution of architecture

database.md:
- Schema design
- Index decisions
- Data modeling trade-offs

api.md:
- API contracts
- Authentication
- Error conventions

decisions.md:
- Important engineering decisions
- Alternatives
- Trade-offs

learning_notes.md:
- Concepts learned
- Production practices
- Problems and solutions

troubleshooting.md:
- Bugs
- Root causes
- Debugging process

After completing major features:
- Update relevant documentation.
- Focus on reasoning, not implementation details.

Prefer small incremental changes. 
Do not generate large amounts of code in one response.
