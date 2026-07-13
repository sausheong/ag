# ag

Make an agent out of anything. **ag** wraps your CLIs and apps into a conversational interface, then learns and evolves — rewriting its own skills after every use so it gets better without you having to intervene.

## How it works

1. Declare your tools in `ag.yaml` — binary path, URL, or MCP server
2. Run `ag bootstrap` — ag generates full interface specs from `--help` output or live page scraping
3. Talk to your tools in plain English — `ag "do this thing"` or `ag` for an interactive session
4. ag learns: after every tool call it reviews the output, updates its own specs, and saves new skills

## Installation

```bash
git clone https://github.com/sausheong/ag
cd ag
make build          # produces ./ag
make install        # installs to $GOPATH/bin
```

Requires Go 1.25.1+.

---

## Quick start

```bash
ag bootstrap        # creates ag.yaml skeleton if missing
```

Edit `ag.yaml` — add your tools and credentials:

```yaml
model: anthropic/claude-sonnet-4-5
auth_token: sk-ant-...
base_url: https://your-litellm-proxy.example.com  # optional
tools:
  - name: git
    bin: git
    hint: focus on status, log, and diff
skills: []
```

Then:

```bash
ag bootstrap        # enriches tool entries with descriptions, parameters, examples
ag                  # interactive REPL
ag "show git log"   # one-shot
ag help             # all commands
ag -c path/to/ag.yaml   # use a specific config file
ag add tool jq /usr/bin/jq "JSON processor"
ag add web hackernews https://news.ycombinator.com "top stories"
ag add mcp filesystem --command npx --args "-y,@modelcontextprotocol/server-filesystem,/tmp"
```

---

## ag.yaml reference

`ag.yaml` is the single source of truth — credentials, tools, MCP servers, and skills.

### Structure

```yaml
model: anthropic/claude-sonnet-4-5
auth_token: sk-ant-...
base_url:            # optional: litellm or custom Anthropic-compatible proxy
tools:
  - name: photos
    bin: /path/to/photos
    hint: search and export from the macOS Photos library
  - name: bash
    bin: /bin/bash
    prefix_args: [-c]
  - name: hackernews
    url: https://news.ycombinator.com
    hint: top stories, search, comments
mcp_servers:
  - name: filesystem
    command: npx
    args: [-y, "@modelcontextprotocol/server-filesystem", /tmp]
  - name: myapi
    url: https://my-mcp-server.example.com/mcp
    headers:
      Authorization: "Bearer token123"
skills:
  - name: deploy-flow
    type: macro
    body: "run build then rsync to staging"
```

### tools

Each tool entry starts minimal and bootstrap enriches it. ag preserves `bin`, `url`, `hint`, `prefix_args`, `headless`, and `profile` on every self-update — the agent can never change how a tool is invoked.

| Field | Who writes it | What it does |
|-------|--------------|-------------|
| `name` | You | Tool identifier |
| `bin` | You | Path to executable (CLI tool) |
| `url` | You | URL of the web app to control (web tool) |
| `hint` | You (optional) | Guides bootstrap |
| `prefix_args` | You (optional) | Fixed args prepended before user args: `bash -c`, `python3 -c` |
| `headless` | You (optional) | Run browser hidden; default `false` |
| `profile` | You (optional) | Persistent browser profile name; stored in `~/.ag/profiles/<name>/` |
| `description` | Bootstrap | What the tool does |
| `parameters` | Bootstrap | JSON Schema of all flags and args |
| `positional` | Bootstrap | Which params are positional, in order |
| `examples` | Bootstrap | 2-3 example invocations |

**CLI tools** — set `bin:`. Bootstrap runs `<bin>`, `<bin> --help`, and `<bin> help` and generates the full spec.

**Web tools** — set `url:` instead of `bin:`. Bootstrap opens the browser, scrapes the page, and generates named action specs (e.g. `web_hackernews_list_top_stories`). For sites requiring login, run `ag web login <name>` once to save the session. Good candidates: internal dashboards, company wikis, legacy apps, public sites. For services with OAuth MCP servers (Gmail, GitHub, Notion), use MCP instead.

**Shell/interpreter tools using `prefix_args`:**

```yaml
- name: bash
  bin: /bin/bash
  prefix_args: [-c]

- name: python
  bin: /usr/bin/python3
  prefix_args: [-c]
```

### mcp_servers

MCP servers connect automatically at startup. Their tools appear as `mcp__<name>__<tool>`.

| Field | Description |
|-------|-------------|
| `name` | Server identifier |
| `command` | Binary for stdio transport |
| `args` | Arguments for stdio transport |
| `env` | Extra env vars for the subprocess |
| `url` | URL for HTTP/SSE transport |
| `headers` | HTTP headers (e.g. auth tokens) |

### skills

- **context** — prepended to the system prompt on every turn
- **macro** — a named workflow the agent can replay by name

### Credential resolution

`ag.yaml` wins. Env vars are fallback only:

| Setting | ag.yaml field | Env var fallback |
|---------|--------------|-----------------|
| Model | `model` | `ANTHROPIC_MODEL` |
| API key | `auth_token` | `ANTHROPIC_AUTH_TOKEN` → `ANTHROPIC_API_KEY` |
| Base URL | `base_url` | `ANTHROPIC_BASE_URL` |

`.env` in the current directory is loaded automatically.

---

## Commands

| Command | Description |
|---------|-------------|
| `ag` | Show help (no ag.yaml) or interactive REPL (with ag.yaml) |
| `ag help` / `ag --help` | Show all commands |
| `ag "request"` | One-shot — run one request and exit |
| `ag bootstrap` | Create `ag.yaml` skeleton or enrich tool specs |
| `ag skills` | List learned skills |
| `ag add tool <name> <bin> [hint]` | Add a CLI tool and bootstrap it |
| `ag add web <name> <url> [hint]` | Add a web app tool and bootstrap it |
| `ag web login <name>` | Open browser to log into a web tool and save the session |
| `ag add skill <name> <body> [context\|macro]` | Add or update a skill |
| `ag add mcp <name> --command <cmd> [--args a,b]` | Add a stdio MCP server |
| `ag add mcp <name> --url <url> [--header K=V]` | Add an HTTP MCP server |
| `ag --config <path>` / `ag -c <path>` | Use a specific ag.yaml |

**REPL commands:** `/help`, `/exit`, `/config`, `/tools`, `/skills`, `/mcp`

The REPL reloads `ag.yaml` automatically after each turn when the agent modifies it.

---

## Self-evolution

After every successful tool call, ag:

1. Reviews the output for new patterns not in the current spec
2. Calls `config_manage update_tool` to update the description, parameters, or examples
3. Calls `skill_manage` to save a context skill if a reusable workflow was found

Built-in tools: **`config_manage`**, **`skill_manage`**, **`cli_run_<name>`**, **`web_<name>_<action>`**, **`mcp__<server>__<tool>`**

---

## Examples

- **`examples/photos/`** — macOS Photos + ImageMagick + bash. CLI tools with `prefix_args`, positional subcommands, compiled Swift binary.
- **`examples/hackernews/`** — Hacker News as a web tool. No login. Bootstrap generates 4 actions from the live page.

```bash
# photos
ag -c examples/photos/ag.yaml
> show me photos from July 2024
> export the beach photos to ~/Desktop

# hackernews
ag -c examples/hackernews/ag.yaml
> what are the top stories right now?
> what does HN think about Go 1.25?
```

---

## Development

```bash
make build             # build ./ag
make test              # go test ./...
make test-integration  # integration smoke tests (requires API key)
make vet               # go vet ./...
make install           # go install to $GOPATH/bin
```

## Architecture

```
ag (binary)
├── config/       — ag.yaml schema: ToolSpec, MCPServerConfig, SkillEntry
├── ui/           — lipgloss styles, LineRenderer (markdown+tables), RenderToolOutput
├── tool/
│   ├── cli.go         — CLITool: prefix_args, argv builder, streaming output
│   ├── web.go         — WebTool: Playwright browser automation, action dispatch
│   ├── web_profile.go — persistent Chrome profiles, cookie helpers
│   ├── config.go      — ConfigManageTool: reads/writes ag.yaml
│   └── skill.go       — JSONSkillStore + SkillProviderAdapter
├── bootstrap/    — help extraction (CLI) + page scraping (web), LLM prompts
├── agent/        — harness runtime, REPL, self-learning hooks
├── main.go       — CLI routing: bootstrap, add tool/web/skill/mcp, web login
└── examples/
    ├── photos/        — macOS Photos + ImageMagick + bash
    └── hackernews/    — Hacker News web tool
```

Built on [`github.com/sausheong/harness`](https://github.com/sausheong/harness).
