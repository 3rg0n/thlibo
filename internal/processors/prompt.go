package processors

// Prompt processor dispatch: frontmatter fields become the daemon request
// config (temperature, max_tokens, etc.); the markdown body is the system
// prompt sent as-is to the daemon.
