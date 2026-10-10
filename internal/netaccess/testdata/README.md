# netaccess fixtures

Responses of the Tailscale LocalAPI (`/localapi/v0/status`, `/localapi/v0/whois`).

**Provenance.** They were produced by serialising Tailscale's own Go types (`ipnstate.Status`, `apitype.WhoIsResponse`, `tailcfg.Node`, `tailcfg.UserProfile` from `tailscale.com@v1.102.5`) with `encoding/json`, which is exactly what its LocalAPI sends, filled with invented names (`owner@example.com`, `vps.tail1234.ts.net`). They are **not captures from a live tailscaled**: the development machine has no Tailscale. The owner's tailnet checklist (`docs/research/tailnet-spike.md`) asks for a real capture to be compared with them.

| File | What it is |
| --- | --- |
| `localapi-status.json` | A running node owned by a person |
| `localapi-status-stopped.json` | Tailscale installed but not running |
| `localapi-status-tagged.json` | A running node that belongs to a tag, not a person |
| `localapi-whois-person.json` | A peer that belongs to a person (login in mixed case on purpose) |
| `localapi-whois-tagged.json` | A peer that belongs to a tag |

To regenerate: a throwaway program in a scratch module that imports those types and writes the files (`json.MarshalIndent`).
