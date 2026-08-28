# MyPA Roadmap & Planning

This document tracks upcoming features, technical debt, and future improvements for the MyPA project.

## V3 (Upcoming Major Release)

### Initial Missions & Pre-requisites
1. **Define Development Conventions**
   - Establish strict agent rules as the very first mission for V3 development. (Completed)
2. **Verify README**
   - Review and fix mistakes in the README to ensure it is accurate. (Completed)

### Features
1. **Gmail arrangement and data extraction to trigger automated flows**
   - For example: if a receipt is received, organize it.
   - Create events based on emails.
   - Suggest events based on emails.
   - Suggest responses.
   - Suggest deleting, blocking, or removing subscriptions, etc.
2. **Bigger Whatsapp options**
   - Connect to groups, etc.
3. ~~**Multi-user Support**~~ (Completed)
   - ~~Create another instance for my wife to use on her phone.~~ (Implemented via Multi-Tenant Architecture in V3)
4. **Enhance Cognitive Capabilities**
   - Improve brain, memory, and learning process.
5. **Connect to remote files / Project Management**
   - For example: "I need to add to 'X' project a 'Y' feature" - beginning with just listing the need, later connecting to GitHub.
6. **Granular Feature Configuration**
   - Add optional configuration settings so users can enable only the specific features they want to use.
7. **Shopping List & General Lists Management**
   - Add the ability to create, update, and manage various lists (e.g., shopping lists, packing lists) naturally through conversation.
8. **Cooking Recommendations & Saved Recipes**
   - Manage saved recipes and provide cooking recommendations (requirements to be expanded).

### Suggested Quality of Life (QoL) Features
1. **Automated Expense & Budget Tracking**: Categorize receipts from Gmail, calculate monthly spending against budgets, split shared expenses, and send financial summaries.
2. **Proactive Relationship Management (Personal CRM)**: Monitor communication frequency and remind the user to reach out to important contacts (e.g., birthdays, check-ins).
3. **Smart Scheduling & Contextual Time-Blocking**: Analyze workload to automatically block out "Deep Work" time and suggest rescheduling options for conflicts.
4. **Unified "Universal Search"**: A single search command that spans Gmail, local files, WhatsApp history, and remote GitHub repositories.
5. **Voice Note Processing & Meeting Summarization**: Transcribe WhatsApp voice notes, extract actionable tasks, create calendar events, and organize thoughts into the memory system.
6. ~~**"Read-It-Later" & Content Summarization**: Summarize forwarded articles/videos into key takeaways and include them in Morning Briefs or a learning database.~~ (Completed)
7. **Geofenced Automations**: Trigger location-based actions (e.g., messaging a spouse when leaving work, or grocery reminders).
8. **Health & Habit Check-ins**: Interactive WhatsApp messages to help build and track habits (water intake, workouts) based on optimal timing.
9. **Smart Travel & Itinerary Builder**: Automatically build itineraries from booking confirmations, check weather, suggest packing lists, and look up local events.
10. **Shared Family Dashboard**: A synchronized space for the multi-user instances to share grocery lists, coordinate schedules, and assign household tasks.

---

## Backlog & Tech Debt (Future Ideas)

- **Cloud Deployment & CI/CD**: Move from local development to a 24/7 cloud environment. Set up GitHub Actions for automated linting, testing, and deployment. Deploy the dockerized microservice stack to a cloud provider.
- ~~**Google Tasks: Multiple Lists**: Currently, the assistant only interfaces with the user's `@default` task list. Expand this to allow the assistant to list, switch between, and manage multiple Google Task lists.~~ (Completed)
- ~~**Improve Response Parsing (Markdown Conversion)**: Ensure LLM responses with complex formatting (like markdown bullet points or tables) translate cleanly across all messaging platforms (Telegram/WhatsApp). Consider building a robust markdown-to-platform-native parser.~~ (Completed)
- **Proxy Replacement**: Consider replacing the custom Go proxy service with an industry-standard ingress controller (e.g., Nginx, Traefik, or Caddy) to benefit from built-in rate limiting, SSL termination, and load balancing.
- **API Documentation**: Generate Swagger/OpenAPI specifications or a Postman collection to clearly document the JSON payloads expected by the webhooks.
- ~~**Daily Blast Pending Tasks**: Add the pending TODO tasks in the daily blast.~~ (Completed)
