# Project Instructions

## Project Context
- This repository is the Vietnamese fork of voocel/ainovel-cli.
- Preserve Vietnamese localization for user-facing UI, prompts, docs, and examples.

## Workflow
- Keep changes small and scoped.
- Do not broad refactor or mass format unless explicitly requested.
- Push work to the user's fork (`origin`) when asked; do not create PRs to upstream unless explicitly requested.

## Docker / Testing
- Prefer Docker-based Go commands instead of relying on host Go.
- Suggested test command: `docker run --rm -v "$PWD:/src" -w /src golang:1.25 go test ./...`
- Build image with: `docker build -t ainovel-cli-vi .`

## Story Workspaces
- Treat one workspace directory as one story.
- Prefer story-named slug directories, e.g. `trong-sinh-roi-toi-dung-tam-thanh-lat-tung-hao-mon/`.
- Docker should mount the chosen story workspace to `/workspace`.
- Do not delete, move, or overwrite story workspace data unless explicitly requested.

## Sensitive / User Data
- Do not modify `config/`, secrets, credentials, API keys, or generated story outputs unless explicitly requested.
- Before deleting or overwriting user data, inspect and confirm.

## Localization
- When pulling or adding user-facing text, translate to Vietnamese unless the text is code, API names, or protocol details that must remain unchanged.
