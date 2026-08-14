# Future Ideas & Improvements

This document tracks technical debt, feature ideas, and minor improvements that we've decided to defer to a later date.

- **Google Tasks: Multiple Lists**: Currently, the assistant only interfaces with the user's `@default` task list. We should expand this to allow the assistant to list, switch between, and manage multiple Google Task lists.
- **Improve Response Parsing (Markdown Conversion)**: Ensure LLM responses with complex formatting (like markdown bullet points or tables) translate cleanly across all messaging platforms (Telegram/WhatsApp). Consider building a robust markdown-to-platform-native parser.
