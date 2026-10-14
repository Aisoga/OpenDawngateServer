# Design decisions

Short records of the project's larger design decisions: what was decided and why. Code comments refer to them as ADR-1, ADR-3 and so on.

| # | Decision |
|---|---|
| [1](0001-languages.md) | Go for the server, C++ for the match host, Python for the developers' setup and tests |
| 2 | The match host's internal design. Not published, because it consists mostly of addresses inside the game's exe. Its outcome is described in [how-it-works.md](../../how-it-works.md): one game process per match, with this project's DLL running the game's own simulation as the server. |
| [3](0003-setup-in-go.md) | The release's setup in Go, so players and operators need no developer tools |
| [4](0004-outside-go-modules.md) | The few outside Go modules allowed (SQLite, `golang.org/x/crypto`), and how they're pinned |
