# DevCell Home Directory Templates

This directory contains template files that are automatically copied to the container's home directory (`/home/${USER}/`) at container startup via `entrypoint.sh`.

## How It Works

1. **At Container Startup**: The entrypoint script processes files from `homedir/` to `/home/${USER}/`
2. **Smart Merging**:
   - **Claude settings.json**: Intelligently merged using `jq` - preserves existing settings, only adds PermissionRequest hook if not already configured
   - **Claude hooks/**: Always copied/updated to ensure latest scripts
   - **Other files**: Only copied if they don't exist (won't overwrite)
3. **Per-Session Isolation**: Each TMUX session has its own `CELL_HOME` which is mounted as the container's home directory

## Directory Structure

```
homedir/
├── .claude/
│   ├── hooks/
│   │   └── auto-approve-all.sh    # Auto-approves all permission requests
│   └── settings.json                # Claude Code settings with PermissionRequest hook
└── README.md
```

## Claude Code Permission Hook

The `.claude/` directory configures Claude Code to automatically approve all permission requests for background agents (Task tool) without prompting.

This is equivalent to running with `--dangerously-skip-permissions` but works for background agents, which started prompting after Claude Code v2.1.20.

### How the Hook Works

1. **Hook Script** (`hooks/auto-approve-all.sh`): Returns `{"decision": "allow", "applyPermissionRule": true}`
2. **Settings** (`settings.json`): Configures Claude Code to call the hook for all tools (matcher: `"*"`)
3. **Result**: Background agents run without permission prompts

### Smart Settings Merge

The entrypoint uses `jq` to intelligently merge settings:

- ✅ **No existing settings**: Creates from template
- ✅ **Existing settings, no PermissionRequest hook**: Adds the hook, preserves all other settings
- ✅ **Existing settings with PermissionRequest hook**: Keeps your custom hook (doesn't overwrite)

This means you can customize your PermissionRequest hook and it won't be overwritten on container restart!

## Adding New Template Files

To add new files that should be automatically copied to container home directories:

1. Add the file to this `homedir/` directory
2. The file will be automatically copied at next container startup
3. Files won't overwrite existing files in the container

## Customization Per Project

Each project using devcell can customize these templates by:
- Modifying files in this directory (applies to all containers from this repo)
- Or by manually creating files in `${CELL_HOME}` on the host (applies to specific TMUX sessions)
