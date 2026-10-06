# Skills

This directory holds the five skills: `muda` (entry and routing),
`muda-measure`, `muda-diagnose`, `muda-fix` and `muda-verify`. The same files
are embedded in the muda binary, which installs them explicitly:

```sh
muda skills install --agent claude --scope user
```

Agents are `claude`, `pi` or `codex`; scopes are `user` or `project`.

They also ship through the Claude Code plugin/marketplace route and the pi
package route. Plugin and package installation supplies the skills, not the
binary. Each skill's preflight checks for a compatible muda binary before use.
