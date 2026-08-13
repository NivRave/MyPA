# Personal AI Assistant: Master Plan & V1 MVP

## 1. Product Vision: The End Result
The final product is an omnipresent, autonomous personal agent designed to operate as a digital chief of staff. Rather than a monolithic chatbot, the system is structured as an event-driven microservices architecture. It continuously monitors designated communication channels, triages incoming information, manages calendar conflicts, and executes multi-step tasks across external APIs. 

By utilizing highly concurrent gateways for webhook ingestion and robust message queues to decouple LLM reasoning from API execution, the system can scale to handle real-time voice routing, proactive background processing, and long-term semantic memory retrieval without blocking core execution threads.

## 2. Future Feature Roadmap
* Omni-channel messaging unification (Telegram, WhatsApp)
* Real-time voice interaction interface (WebRTC / Streaming Speech-to-Text)
* Background email triage and automated response drafting
* Long-term semantic memory via Vector Database (Cross-session recall)
* Dynamic context-as-code system prompt injection
* Autonomous background job scheduling and daily reporting
* Secure, isolated credential and API execution workers

---

## 3. The V1 MVP: "The Brain Dump" Capture Bot

**Objective:** 
Establish the foundational end-to-end agentic loop with the lowest possible complexity. V1 will capture unstructured text/voice notes and autonomously convert them into structured calendar events, validating the core reasoning and tool-calling pipeline.

### Scope
* **Input Channel:** Telegram (via Bot API)
* **Action Tool:** Google Calendar API (`create_calendar_event`)
* **Core Interaction:** User sends a natural language request (e.g., "Block two hours for deep work tomorrow morning"), the AI parses the intent, executes the calendar API call, and replies with a confirmation.

### V1 Architecture Stack
* **API Gateway (Webhook Ingestion):** Built in Go (Golang) to ensure a lightweight, highly concurrent front door that instantly acknowledges incoming webhooks.
* **Message Broker:** A basic message queue (e.g., RabbitMQ or Redis Pub/Sub) to pass normalized payloads from the gateway to the reasoning engine, ensuring the gateway is never blocked by slow LLM API responses.
* **Orchestration / Reasoning Layer:** Built in C# or Go, this service consumes messages from the queue, maintains short-term conversational state (via Redis), constructs the prompt with the available tool schema, and triggers the LLM (e.g., OpenAI or Anthropic API).
* **Execution Module:** A straightforward API client that securely holds the Google Workspace OAuth tokens and executes the calendar insertion when instructed by the orchestrator.

### Execution Flow
1. **Ingest:** User messages the Telegram bot. The Telegram webhook hits the Go API Gateway.
2. **Queue:** The Gateway drops the normalized message payload onto the message broker and returns a `200 OK` to Telegram.
3. **Reason:** The Orchestration worker picks up the message, pulls recent chat history from Redis, and sends the prompt to the LLM along with the `create_calendar_event` tool definition.
4. **Act:** The LLM returns a tool-call request with the parsed JSON arguments (title, start time, end time). The backend executes the Google Calendar API call.
5. **Respond:** The backend receives the successful Google API response, sends a confirmation text back to the user via the Telegram API, and updates the short-term memory cache.

### V1 Definition of Done
- A Telegram bot is live and responsive.
- The system correctly extracts relative dates and times from natural language.
- Events successfully appear on the connected Google Calendar.
- The bot replies with an accurate confirmation message upon success or a clear error if the API fails.
