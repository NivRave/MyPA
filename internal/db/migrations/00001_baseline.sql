-- Baseline schema for MyPA (P4-3), generated from the pre-goose AutoMigrate + ApplyCustomIndexes
-- schema with: pg_dump --schema-only --no-owner --no-privileges --no-comments (pgvector/pgvector:pg15).
-- Session-level SET / set_config(search_path) lines and psql \restrict meta-commands were removed:
-- they are not SQL, and search_path changes would otherwise leak onto pooled connections.
-- Existing (pre-goose) databases are ADOPTED at version 1 without running this file (see
-- db.Migrate); fresh databases run it.

-- +goose Up
--
-- PostgreSQL database dump
--

--
-- Name: vector; Type: EXTENSION; Schema: -; Owner: -
--

CREATE EXTENSION IF NOT EXISTS vector WITH SCHEMA public;

--
-- Name: audit_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.audit_events (
    id uuid NOT NULL,
    session_id text,
    event_type text,
    action_name text,
    request_payload jsonb,
    response_payload jsonb,
    error_message text,
    start_time timestamp with time zone,
    end_time timestamp with time zone,
    duration_ms bigint,
    prompt_tokens bigint,
    completion_tokens bigint
);

--
-- Name: audit_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.audit_sessions (
    id uuid NOT NULL,
    user_id text,
    chat_id text,
    source text,
    user_prompt text,
    final_response text,
    start_time timestamp with time zone,
    end_time timestamp with time zone,
    total_duration_ms bigint,
    total_tokens bigint,
    status text,
    tool_summary text
);

--
-- Name: automation_rules; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.automation_rules (
    id bigint NOT NULL,
    user_id text,
    family_group text,
    name text,
    trigger_type text,
    trigger_topic text,
    condition_json jsonb,
    action_instruction text,
    is_active boolean DEFAULT true,
    created_at timestamp with time zone
);

--
-- Name: automation_rules_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.automation_rules_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: automation_rules_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.automation_rules_id_seq OWNED BY public.automation_rules.id;

--
-- Name: baby_care_logs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.baby_care_logs (
    id bigint NOT NULL,
    baby_id bigint,
    family_group text,
    log_type text,
    value_numeric numeric,
    unit text,
    details text,
    "timestamp" timestamp with time zone,
    logged_by_user_id text
);

--
-- Name: baby_care_logs_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.baby_care_logs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: baby_care_logs_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.baby_care_logs_id_seq OWNED BY public.baby_care_logs.id;

--
-- Name: baby_inventory_items; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.baby_inventory_items (
    id bigint NOT NULL,
    family_group text,
    item_name text,
    quantity_remaining numeric,
    unit text,
    low_threshold numeric,
    updated_at timestamp with time zone
);

--
-- Name: baby_inventory_items_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.baby_inventory_items_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: baby_inventory_items_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.baby_inventory_items_id_seq OWNED BY public.baby_inventory_items.id;

--
-- Name: baby_profiles; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.baby_profiles (
    id bigint NOT NULL,
    family_group text,
    name text,
    date_of_birth timestamp with time zone,
    due_date timestamp with time zone,
    gender text,
    birth_weight_grams bigint,
    current_weight_grams bigint,
    height_cm numeric,
    pediatrician_info text,
    allergies_notes text,
    updated_by_user_id text,
    updated_at timestamp with time zone
);

--
-- Name: baby_profiles_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.baby_profiles_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: baby_profiles_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.baby_profiles_id_seq OWNED BY public.baby_profiles.id;

--
-- Name: category_budgets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.category_budgets (
    id bigint NOT NULL,
    family_group text,
    category text,
    monthly_limit numeric,
    currency text,
    alert_threshold_pct numeric,
    created_at timestamp with time zone,
    updated_at timestamp with time zone
);

--
-- Name: category_budgets_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.category_budgets_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: category_budgets_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.category_budgets_id_seq OWNED BY public.category_budgets.id;

--
-- Name: encrypted_credentials; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.encrypted_credentials (
    user_id text NOT NULL,
    provider text NOT NULL,
    ciphertext bytea,
    updated_at timestamp with time zone
);

--
-- Name: entity_profiles; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.entity_profiles (
    id bigint NOT NULL,
    user_id text,
    family_group text,
    topic text,
    entity_type text,
    entity_name text,
    attributes_json jsonb,
    updated_by_user_id text,
    updated_at timestamp with time zone
);

--
-- Name: entity_profiles_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.entity_profiles_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: entity_profiles_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.entity_profiles_id_seq OWNED BY public.entity_profiles.id;

--
-- Name: external_api_connectors; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.external_api_connectors (
    id bigint NOT NULL,
    user_id text,
    family_group text,
    name text,
    base_url text,
    default_method text,
    encrypted_auth_headers bytea,
    description text,
    created_at timestamp with time zone
);

--
-- Name: external_api_connectors_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.external_api_connectors_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: external_api_connectors_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.external_api_connectors_id_seq OWNED BY public.external_api_connectors.id;

--
-- Name: financial_transactions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.financial_transactions (
    id bigint NOT NULL,
    user_id text,
    family_group text,
    type text,
    category text,
    merchant text,
    amount numeric,
    currency text,
    transaction_date timestamp with time zone,
    is_shared boolean DEFAULT true,
    source text,
    notes text,
    created_at timestamp with time zone
);

--
-- Name: financial_transactions_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.financial_transactions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: financial_transactions_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.financial_transactions_id_seq OWNED BY public.financial_transactions.id;

--
-- Name: home_maintenance_items; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.home_maintenance_items (
    id bigint NOT NULL,
    family_group text,
    task_name text,
    category text,
    interval_days bigint,
    last_completed_at timestamp with time zone,
    next_due_at timestamp with time zone,
    contractor_info text,
    notes text,
    updated_by_user_id text
);

--
-- Name: home_maintenance_items_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.home_maintenance_items_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: home_maintenance_items_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.home_maintenance_items_id_seq OWNED BY public.home_maintenance_items.id;

--
-- Name: house_contract_records; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.house_contract_records (
    id bigint NOT NULL,
    family_group text,
    record_type text,
    title text,
    vendor_or_landlord text,
    contact_details text,
    amount numeric,
    currency text,
    start_date timestamp with time zone,
    expiration_or_renewal_date timestamp with time zone,
    notice_period_days bigint,
    key_terms_json jsonb,
    created_by_user_id text,
    created_at timestamp with time zone
);

--
-- Name: house_contract_records_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.house_contract_records_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: house_contract_records_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.house_contract_records_id_seq OWNED BY public.house_contract_records.id;

--
-- Name: idempotency_records; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.idempotency_records (
    key text NOT NULL,
    user_id text,
    tool_name text,
    response_summary text,
    expires_at timestamp with time zone
);

--
-- Name: memories; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.memories (
    id bigint NOT NULL,
    user_id text,
    scope text DEFAULT 'personal'::text,
    fact text,
    embedding public.vector(3072),
    created_at timestamp with time zone
);

--
-- Name: memories_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.memories_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: memories_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.memories_id_seq OWNED BY public.memories.id;

--
-- Name: mutation_logs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.mutation_logs (
    id text NOT NULL,
    user_id text,
    family_group text,
    topic text,
    action text,
    entity_type text,
    entity_id text,
    before_json jsonb,
    after_json jsonb,
    summary text,
    is_undone boolean DEFAULT false,
    created_at timestamp with time zone
);

--
-- Name: pairing_codes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.pairing_codes (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    code text,
    role text,
    family_group text,
    expires_at timestamp with time zone,
    is_used boolean DEFAULT false,
    created_at timestamp with time zone
);

--
-- Name: personal_crm_contacts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.personal_crm_contacts (
    id bigint NOT NULL,
    user_id text,
    family_group text,
    person_name text,
    relationship text,
    important_dates_json jsonb,
    gift_ideas_and_sizes text,
    medical_notes text,
    updated_at timestamp with time zone
);

--
-- Name: personal_crm_contacts_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.personal_crm_contacts_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: personal_crm_contacts_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.personal_crm_contacts_id_seq OWNED BY public.personal_crm_contacts.id;

--
-- Name: recurring_bills; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.recurring_bills (
    id bigint NOT NULL,
    family_group text,
    name text,
    category text,
    expected_amount numeric,
    currency text,
    billing_cycle text,
    next_due_day bigint,
    auto_renew boolean DEFAULT true,
    created_at timestamp with time zone,
    updated_at timestamp with time zone
);

--
-- Name: recurring_bills_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.recurring_bills_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: recurring_bills_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.recurring_bills_id_seq OWNED BY public.recurring_bills.id;

--
-- Name: schedule_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.schedule_events (
    id bigint NOT NULL,
    external_google_event_id text,
    partner_google_event_id text,
    user_id text,
    family_group text,
    topic text,
    tags_json jsonb,
    title text,
    description text,
    start_time timestamp with time zone,
    end_time timestamp with time zone,
    recurrence_rule text,
    sync_to_google boolean DEFAULT true,
    is_shared boolean DEFAULT false,
    created_by_user_id text,
    created_at timestamp with time zone
);

--
-- Name: schedule_events_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.schedule_events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: schedule_events_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.schedule_events_id_seq OWNED BY public.schedule_events.id;

--
-- Name: scheduled_reminders; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.scheduled_reminders (
    id bigint NOT NULL,
    user_id text,
    message text,
    due_time timestamp with time zone,
    is_sent boolean,
    created_at timestamp with time zone
);

--
-- Name: scheduled_reminders_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.scheduled_reminders_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: scheduled_reminders_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.scheduled_reminders_id_seq OWNED BY public.scheduled_reminders.id;

--
-- Name: shared_spaces; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.shared_spaces (
    id bigint NOT NULL,
    family_group character varying(100) NOT NULL,
    topic character varying(100) NOT NULL,
    owner_user_id character varying(100),
    shared_with_user_id character varying(100),
    can_view boolean DEFAULT true,
    can_edit boolean DEFAULT true,
    notify_mode character varying(20) DEFAULT 'instant'::character varying,
    updated_at timestamp with time zone
);

--
-- Name: shared_spaces_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.shared_spaces_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: shared_spaces_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.shared_spaces_id_seq OWNED BY public.shared_spaces.id;

--
-- Name: shared_task_reminders; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.shared_task_reminders (
    id bigint NOT NULL,
    family_group character varying(100) NOT NULL,
    topic character varying(100) NOT NULL,
    title text NOT NULL,
    due_time timestamp with time zone,
    created_by_user_id character varying(100) NOT NULL,
    assigned_to_user_id character varying(100),
    is_completed boolean DEFAULT false,
    completed_by_user_id character varying(100),
    completed_at timestamp with time zone,
    is_sent boolean DEFAULT false,
    reminded_at timestamp with time zone,
    created_at timestamp with time zone
);

--
-- Name: shared_task_reminders_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.shared_task_reminders_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: shared_task_reminders_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.shared_task_reminders_id_seq OWNED BY public.shared_task_reminders.id;

--
-- Name: topic_calendar_configs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.topic_calendar_configs (
    id bigint NOT NULL,
    user_id text,
    topic text,
    color_id text,
    prefix_tag text,
    target_calendar_id text,
    include_in_morning_briefing boolean DEFAULT true,
    sync_to_google_default boolean,
    updated_at timestamp with time zone
);

--
-- Name: topic_calendar_configs_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.topic_calendar_configs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: topic_calendar_configs_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.topic_calendar_configs_id_seq OWNED BY public.topic_calendar_configs.id;

--
-- Name: topic_memories; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.topic_memories (
    id bigint NOT NULL,
    user_id text,
    family_group text,
    topic text,
    subtopic text,
    tags_json jsonb,
    fact text,
    scope text DEFAULT 'personal'::text,
    created_by_user_id text,
    superseded_by_id bigint,
    valid_from timestamp with time zone,
    valid_until timestamp with time zone,
    is_pinned boolean DEFAULT false,
    embedding public.vector(3072),
    created_at timestamp with time zone
);

--
-- Name: topic_memories_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.topic_memories_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: topic_memories_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.topic_memories_id_seq OWNED BY public.topic_memories.id;

--
-- Name: user_preferences; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.user_preferences (
    user_id text NOT NULL,
    preferred_language text,
    tone_persona text,
    default_currency text,
    quiet_hours_start text,
    quiet_hours_end text,
    briefing_topics_json jsonb,
    standing_instructions text,
    updated_at timestamp with time zone
);

--
-- Name: users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.users (
    id uuid DEFAULT gen_random_uuid() NOT NULL,
    platform_id text,
    name text,
    role text,
    family_group text,
    created_at timestamp with time zone
);

--
-- Name: vehicle_assets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.vehicle_assets (
    id bigint NOT NULL,
    family_group text,
    name text,
    license_plate text,
    insurance_expiry timestamp with time zone,
    annual_test_expiry timestamp with time zone,
    last_service_date timestamp with time zone,
    current_mileage_km bigint,
    next_service_mileage_km bigint,
    notes text
);

--
-- Name: vehicle_assets_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.vehicle_assets_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: vehicle_assets_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.vehicle_assets_id_seq OWNED BY public.vehicle_assets.id;

--
-- Name: workflows; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.workflows (
    id bigint NOT NULL,
    user_id text,
    name text,
    cron_expression text,
    instruction text,
    is_active boolean,
    created_at timestamp with time zone
);

--
-- Name: workflows_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.workflows_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

--
-- Name: workflows_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.workflows_id_seq OWNED BY public.workflows.id;

--
-- Name: automation_rules id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.automation_rules ALTER COLUMN id SET DEFAULT nextval('public.automation_rules_id_seq'::regclass);

--
-- Name: baby_care_logs id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.baby_care_logs ALTER COLUMN id SET DEFAULT nextval('public.baby_care_logs_id_seq'::regclass);

--
-- Name: baby_inventory_items id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.baby_inventory_items ALTER COLUMN id SET DEFAULT nextval('public.baby_inventory_items_id_seq'::regclass);

--
-- Name: baby_profiles id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.baby_profiles ALTER COLUMN id SET DEFAULT nextval('public.baby_profiles_id_seq'::regclass);

--
-- Name: category_budgets id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.category_budgets ALTER COLUMN id SET DEFAULT nextval('public.category_budgets_id_seq'::regclass);

--
-- Name: entity_profiles id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.entity_profiles ALTER COLUMN id SET DEFAULT nextval('public.entity_profiles_id_seq'::regclass);

--
-- Name: external_api_connectors id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.external_api_connectors ALTER COLUMN id SET DEFAULT nextval('public.external_api_connectors_id_seq'::regclass);

--
-- Name: financial_transactions id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.financial_transactions ALTER COLUMN id SET DEFAULT nextval('public.financial_transactions_id_seq'::regclass);

--
-- Name: home_maintenance_items id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.home_maintenance_items ALTER COLUMN id SET DEFAULT nextval('public.home_maintenance_items_id_seq'::regclass);

--
-- Name: house_contract_records id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.house_contract_records ALTER COLUMN id SET DEFAULT nextval('public.house_contract_records_id_seq'::regclass);

--
-- Name: memories id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.memories ALTER COLUMN id SET DEFAULT nextval('public.memories_id_seq'::regclass);

--
-- Name: personal_crm_contacts id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.personal_crm_contacts ALTER COLUMN id SET DEFAULT nextval('public.personal_crm_contacts_id_seq'::regclass);

--
-- Name: recurring_bills id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.recurring_bills ALTER COLUMN id SET DEFAULT nextval('public.recurring_bills_id_seq'::regclass);

--
-- Name: schedule_events id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.schedule_events ALTER COLUMN id SET DEFAULT nextval('public.schedule_events_id_seq'::regclass);

--
-- Name: scheduled_reminders id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.scheduled_reminders ALTER COLUMN id SET DEFAULT nextval('public.scheduled_reminders_id_seq'::regclass);

--
-- Name: shared_spaces id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.shared_spaces ALTER COLUMN id SET DEFAULT nextval('public.shared_spaces_id_seq'::regclass);

--
-- Name: shared_task_reminders id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.shared_task_reminders ALTER COLUMN id SET DEFAULT nextval('public.shared_task_reminders_id_seq'::regclass);

--
-- Name: topic_calendar_configs id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.topic_calendar_configs ALTER COLUMN id SET DEFAULT nextval('public.topic_calendar_configs_id_seq'::regclass);

--
-- Name: topic_memories id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.topic_memories ALTER COLUMN id SET DEFAULT nextval('public.topic_memories_id_seq'::regclass);

--
-- Name: vehicle_assets id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.vehicle_assets ALTER COLUMN id SET DEFAULT nextval('public.vehicle_assets_id_seq'::regclass);

--
-- Name: workflows id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workflows ALTER COLUMN id SET DEFAULT nextval('public.workflows_id_seq'::regclass);

--
-- Name: audit_events audit_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_events
    ADD CONSTRAINT audit_events_pkey PRIMARY KEY (id);

--
-- Name: audit_sessions audit_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.audit_sessions
    ADD CONSTRAINT audit_sessions_pkey PRIMARY KEY (id);

--
-- Name: automation_rules automation_rules_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.automation_rules
    ADD CONSTRAINT automation_rules_pkey PRIMARY KEY (id);

--
-- Name: baby_care_logs baby_care_logs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.baby_care_logs
    ADD CONSTRAINT baby_care_logs_pkey PRIMARY KEY (id);

--
-- Name: baby_inventory_items baby_inventory_items_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.baby_inventory_items
    ADD CONSTRAINT baby_inventory_items_pkey PRIMARY KEY (id);

--
-- Name: baby_profiles baby_profiles_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.baby_profiles
    ADD CONSTRAINT baby_profiles_pkey PRIMARY KEY (id);

--
-- Name: category_budgets category_budgets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.category_budgets
    ADD CONSTRAINT category_budgets_pkey PRIMARY KEY (id);

--
-- Name: encrypted_credentials encrypted_credentials_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.encrypted_credentials
    ADD CONSTRAINT encrypted_credentials_pkey PRIMARY KEY (user_id, provider);

--
-- Name: entity_profiles entity_profiles_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.entity_profiles
    ADD CONSTRAINT entity_profiles_pkey PRIMARY KEY (id);

--
-- Name: external_api_connectors external_api_connectors_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.external_api_connectors
    ADD CONSTRAINT external_api_connectors_pkey PRIMARY KEY (id);

--
-- Name: financial_transactions financial_transactions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.financial_transactions
    ADD CONSTRAINT financial_transactions_pkey PRIMARY KEY (id);

--
-- Name: home_maintenance_items home_maintenance_items_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.home_maintenance_items
    ADD CONSTRAINT home_maintenance_items_pkey PRIMARY KEY (id);

--
-- Name: house_contract_records house_contract_records_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.house_contract_records
    ADD CONSTRAINT house_contract_records_pkey PRIMARY KEY (id);

--
-- Name: idempotency_records idempotency_records_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.idempotency_records
    ADD CONSTRAINT idempotency_records_pkey PRIMARY KEY (key);

--
-- Name: memories memories_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.memories
    ADD CONSTRAINT memories_pkey PRIMARY KEY (id);

--
-- Name: mutation_logs mutation_logs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mutation_logs
    ADD CONSTRAINT mutation_logs_pkey PRIMARY KEY (id);

--
-- Name: pairing_codes pairing_codes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.pairing_codes
    ADD CONSTRAINT pairing_codes_pkey PRIMARY KEY (id);

--
-- Name: personal_crm_contacts personal_crm_contacts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.personal_crm_contacts
    ADD CONSTRAINT personal_crm_contacts_pkey PRIMARY KEY (id);

--
-- Name: recurring_bills recurring_bills_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.recurring_bills
    ADD CONSTRAINT recurring_bills_pkey PRIMARY KEY (id);

--
-- Name: schedule_events schedule_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.schedule_events
    ADD CONSTRAINT schedule_events_pkey PRIMARY KEY (id);

--
-- Name: scheduled_reminders scheduled_reminders_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.scheduled_reminders
    ADD CONSTRAINT scheduled_reminders_pkey PRIMARY KEY (id);

--
-- Name: shared_spaces shared_spaces_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.shared_spaces
    ADD CONSTRAINT shared_spaces_pkey PRIMARY KEY (id);

--
-- Name: shared_task_reminders shared_task_reminders_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.shared_task_reminders
    ADD CONSTRAINT shared_task_reminders_pkey PRIMARY KEY (id);

--
-- Name: topic_calendar_configs topic_calendar_configs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.topic_calendar_configs
    ADD CONSTRAINT topic_calendar_configs_pkey PRIMARY KEY (id);

--
-- Name: topic_memories topic_memories_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.topic_memories
    ADD CONSTRAINT topic_memories_pkey PRIMARY KEY (id);

--
-- Name: user_preferences user_preferences_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_preferences
    ADD CONSTRAINT user_preferences_pkey PRIMARY KEY (user_id);

--
-- Name: users users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (id);

--
-- Name: vehicle_assets vehicle_assets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.vehicle_assets
    ADD CONSTRAINT vehicle_assets_pkey PRIMARY KEY (id);

--
-- Name: workflows workflows_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.workflows
    ADD CONSTRAINT workflows_pkey PRIMARY KEY (id);

--
-- Name: idx_audit_events_session_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_events_session_id ON public.audit_events USING btree (session_id);

--
-- Name: idx_audit_sessions_start_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_sessions_start_time ON public.audit_sessions USING btree (start_time);

--
-- Name: idx_audit_sessions_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_sessions_user_id ON public.audit_sessions USING btree (user_id);

--
-- Name: idx_audit_sessions_user_start; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_audit_sessions_user_start ON public.audit_sessions USING btree (user_id, start_time DESC);

--
-- Name: idx_automation_rules_family_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_automation_rules_family_group ON public.automation_rules USING btree (family_group);

--
-- Name: idx_automation_rules_is_active; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_automation_rules_is_active ON public.automation_rules USING btree (is_active);

--
-- Name: idx_automation_rules_trigger_topic; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_automation_rules_trigger_topic ON public.automation_rules USING btree (trigger_topic);

--
-- Name: idx_automation_rules_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_automation_rules_user_id ON public.automation_rules USING btree (user_id);

--
-- Name: idx_baby_care_logs_baby_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_baby_care_logs_baby_id ON public.baby_care_logs USING btree (baby_id);

--
-- Name: idx_baby_care_logs_baby_timestamp; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_baby_care_logs_baby_timestamp ON public.baby_care_logs USING btree (baby_id, "timestamp" DESC);

--
-- Name: idx_baby_care_logs_family_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_baby_care_logs_family_group ON public.baby_care_logs USING btree (family_group);

--
-- Name: idx_baby_care_logs_family_timestamp; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_baby_care_logs_family_timestamp ON public.baby_care_logs USING btree (family_group, "timestamp" DESC);

--
-- Name: idx_baby_care_logs_log_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_baby_care_logs_log_type ON public.baby_care_logs USING btree (log_type);

--
-- Name: idx_baby_care_logs_lookup; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_baby_care_logs_lookup ON public.baby_care_logs USING btree (family_group, baby_id, log_type, "timestamp" DESC);

--
-- Name: idx_baby_care_logs_timestamp; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_baby_care_logs_timestamp ON public.baby_care_logs USING btree ("timestamp");

--
-- Name: idx_baby_inventory_items_family_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_baby_inventory_items_family_group ON public.baby_inventory_items USING btree (family_group);

--
-- Name: idx_baby_profiles_family_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_baby_profiles_family_group ON public.baby_profiles USING btree (family_group);

--
-- Name: idx_category_budgets_family_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_category_budgets_family_group ON public.category_budgets USING btree (family_group);

--
-- Name: idx_entity_profiles_entity_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entity_profiles_entity_name ON public.entity_profiles USING btree (entity_name);

--
-- Name: idx_entity_profiles_entity_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entity_profiles_entity_type ON public.entity_profiles USING btree (entity_type);

--
-- Name: idx_entity_profiles_family_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entity_profiles_family_group ON public.entity_profiles USING btree (family_group);

--
-- Name: idx_entity_profiles_topic; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entity_profiles_topic ON public.entity_profiles USING btree (topic);

--
-- Name: idx_entity_profiles_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_entity_profiles_user_id ON public.entity_profiles USING btree (user_id);

--
-- Name: idx_external_api_connectors_family_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_external_api_connectors_family_group ON public.external_api_connectors USING btree (family_group);

--
-- Name: idx_external_api_connectors_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_external_api_connectors_name ON public.external_api_connectors USING btree (name);

--
-- Name: idx_external_api_connectors_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_external_api_connectors_user_id ON public.external_api_connectors USING btree (user_id);

--
-- Name: idx_fam_cat; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_fam_cat ON public.category_budgets USING btree (family_group, category);

--
-- Name: idx_financial_transactions_category; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_financial_transactions_category ON public.financial_transactions USING btree (category);

--
-- Name: idx_financial_transactions_fam_cat; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_financial_transactions_fam_cat ON public.financial_transactions USING btree (family_group, category);

--
-- Name: idx_financial_transactions_fam_type_cat_date; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_financial_transactions_fam_type_cat_date ON public.financial_transactions USING btree (family_group, type, category, transaction_date);

--
-- Name: idx_financial_transactions_family_date; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_financial_transactions_family_date ON public.financial_transactions USING btree (family_group, transaction_date DESC);

--
-- Name: idx_financial_transactions_family_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_financial_transactions_family_group ON public.financial_transactions USING btree (family_group);

--
-- Name: idx_financial_transactions_merchant; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_financial_transactions_merchant ON public.financial_transactions USING btree (merchant);

--
-- Name: idx_financial_transactions_transaction_date; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_financial_transactions_transaction_date ON public.financial_transactions USING btree (transaction_date);

--
-- Name: idx_financial_transactions_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_financial_transactions_type ON public.financial_transactions USING btree (type);

--
-- Name: idx_financial_transactions_user_date; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_financial_transactions_user_date ON public.financial_transactions USING btree (user_id, transaction_date DESC);

--
-- Name: idx_financial_transactions_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_financial_transactions_user_id ON public.financial_transactions USING btree (user_id);

--
-- Name: idx_home_maintenance_items_family_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_home_maintenance_items_family_group ON public.home_maintenance_items USING btree (family_group);

--
-- Name: idx_home_maintenance_items_next_due_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_home_maintenance_items_next_due_at ON public.home_maintenance_items USING btree (next_due_at);

--
-- Name: idx_house_contract_records_expiration_or_renewal_date; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_house_contract_records_expiration_or_renewal_date ON public.house_contract_records USING btree (expiration_or_renewal_date);

--
-- Name: idx_house_contract_records_family_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_house_contract_records_family_group ON public.house_contract_records USING btree (family_group);

--
-- Name: idx_house_contract_records_record_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_house_contract_records_record_type ON public.house_contract_records USING btree (record_type);

--
-- Name: idx_house_contracts_fam_exp; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_house_contracts_fam_exp ON public.house_contract_records USING btree (family_group, expiration_or_renewal_date);

--
-- Name: idx_idempotency_records_expires_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_idempotency_records_expires_at ON public.idempotency_records USING btree (expires_at);

--
-- Name: idx_idempotency_records_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_idempotency_records_user_id ON public.idempotency_records USING btree (user_id);

--
-- Name: idx_memories_embedding; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_memories_embedding ON public.memories USING hnsw (((embedding)::public.halfvec(3072)) public.halfvec_cosine_ops);

--
-- Name: idx_memories_scope; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_memories_scope ON public.memories USING btree (scope);

--
-- Name: idx_memories_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_memories_user_id ON public.memories USING btree (user_id);

--
-- Name: idx_mutation_logs_created_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mutation_logs_created_at ON public.mutation_logs USING btree (created_at);

--
-- Name: idx_mutation_logs_entity_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mutation_logs_entity_id ON public.mutation_logs USING btree (entity_id);

--
-- Name: idx_mutation_logs_entity_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mutation_logs_entity_type ON public.mutation_logs USING btree (entity_type);

--
-- Name: idx_mutation_logs_fam_undone_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mutation_logs_fam_undone_created ON public.mutation_logs USING btree (family_group, is_undone, created_at DESC);

--
-- Name: idx_mutation_logs_family_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mutation_logs_family_created ON public.mutation_logs USING btree (family_group, created_at DESC);

--
-- Name: idx_mutation_logs_family_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mutation_logs_family_group ON public.mutation_logs USING btree (family_group);

--
-- Name: idx_mutation_logs_family_topic_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mutation_logs_family_topic_created ON public.mutation_logs USING btree (family_group, topic, created_at DESC);

--
-- Name: idx_mutation_logs_is_undone; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mutation_logs_is_undone ON public.mutation_logs USING btree (is_undone);

--
-- Name: idx_mutation_logs_topic; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mutation_logs_topic ON public.mutation_logs USING btree (topic);

--
-- Name: idx_mutation_logs_user_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mutation_logs_user_created ON public.mutation_logs USING btree (user_id, created_at DESC);

--
-- Name: idx_mutation_logs_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mutation_logs_user_id ON public.mutation_logs USING btree (user_id);

--
-- Name: idx_pairing_codes_code; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_pairing_codes_code ON public.pairing_codes USING btree (code);

--
-- Name: idx_personal_crm_contacts_family_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_personal_crm_contacts_family_group ON public.personal_crm_contacts USING btree (family_group);

--
-- Name: idx_personal_crm_contacts_person_name; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_personal_crm_contacts_person_name ON public.personal_crm_contacts USING btree (person_name);

--
-- Name: idx_personal_crm_contacts_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_personal_crm_contacts_user_id ON public.personal_crm_contacts USING btree (user_id);

--
-- Name: idx_recurring_bills_family_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_recurring_bills_family_group ON public.recurring_bills USING btree (family_group);

--
-- Name: idx_schedule_events_end_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_schedule_events_end_time ON public.schedule_events USING btree (end_time);

--
-- Name: idx_schedule_events_external_google_event_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_schedule_events_external_google_event_id ON public.schedule_events USING btree (external_google_event_id);

--
-- Name: idx_schedule_events_fam_topic_start; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_schedule_events_fam_topic_start ON public.schedule_events USING btree (family_group, topic, start_time);

--
-- Name: idx_schedule_events_family_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_schedule_events_family_group ON public.schedule_events USING btree (family_group);

--
-- Name: idx_schedule_events_family_start; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_schedule_events_family_start ON public.schedule_events USING btree (family_group, start_time);

--
-- Name: idx_schedule_events_partner_google_event_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_schedule_events_partner_google_event_id ON public.schedule_events USING btree (partner_google_event_id);

--
-- Name: idx_schedule_events_start_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_schedule_events_start_time ON public.schedule_events USING btree (start_time);

--
-- Name: idx_schedule_events_topic; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_schedule_events_topic ON public.schedule_events USING btree (topic);

--
-- Name: idx_schedule_events_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_schedule_events_user_id ON public.schedule_events USING btree (user_id);

--
-- Name: idx_schedule_events_user_start; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_schedule_events_user_start ON public.schedule_events USING btree (user_id, start_time);

--
-- Name: idx_scheduled_reminders_due_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_scheduled_reminders_due_time ON public.scheduled_reminders USING btree (due_time);

--
-- Name: idx_scheduled_reminders_is_sent; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_scheduled_reminders_is_sent ON public.scheduled_reminders USING btree (is_sent);

--
-- Name: idx_scheduled_reminders_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_scheduled_reminders_user_id ON public.scheduled_reminders USING btree (user_id);

--
-- Name: idx_shared_spaces_family_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_shared_spaces_family_group ON public.shared_spaces USING btree (family_group);

--
-- Name: idx_shared_spaces_topic; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_shared_spaces_topic ON public.shared_spaces USING btree (topic);

--
-- Name: idx_shared_task_reminders_due_completed; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_shared_task_reminders_due_completed ON public.shared_task_reminders USING btree (due_time, is_completed);

--
-- Name: idx_shared_task_reminders_due_time; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_shared_task_reminders_due_time ON public.shared_task_reminders USING btree (due_time);

--
-- Name: idx_shared_task_reminders_family_completed_due; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_shared_task_reminders_family_completed_due ON public.shared_task_reminders USING btree (family_group, is_completed, due_time);

--
-- Name: idx_shared_task_reminders_family_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_shared_task_reminders_family_group ON public.shared_task_reminders USING btree (family_group);

--
-- Name: idx_shared_task_reminders_is_completed; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_shared_task_reminders_is_completed ON public.shared_task_reminders USING btree (is_completed);

--
-- Name: idx_shared_task_reminders_is_sent; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_shared_task_reminders_is_sent ON public.shared_task_reminders USING btree (is_sent);

--
-- Name: idx_shared_task_reminders_topic; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_shared_task_reminders_topic ON public.shared_task_reminders USING btree (topic);

--
-- Name: idx_topic_memories_embedding; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_topic_memories_embedding ON public.topic_memories USING hnsw (((embedding)::public.halfvec(3072)) public.halfvec_cosine_ops);

--
-- Name: idx_topic_memories_family_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_topic_memories_family_created ON public.topic_memories USING btree (family_group, created_at DESC);

--
-- Name: idx_topic_memories_family_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_topic_memories_family_group ON public.topic_memories USING btree (family_group);

--
-- Name: idx_topic_memories_scope; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_topic_memories_scope ON public.topic_memories USING btree (scope);

--
-- Name: idx_topic_memories_subtopic; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_topic_memories_subtopic ON public.topic_memories USING btree (subtopic);

--
-- Name: idx_topic_memories_superseded_by_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_topic_memories_superseded_by_id ON public.topic_memories USING btree (superseded_by_id);

--
-- Name: idx_topic_memories_topic; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_topic_memories_topic ON public.topic_memories USING btree (topic);

--
-- Name: idx_topic_memories_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_topic_memories_user_id ON public.topic_memories USING btree (user_id);

--
-- Name: idx_user_topic; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_user_topic ON public.topic_calendar_configs USING btree (user_id, topic);

--
-- Name: idx_users_platform_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_users_platform_id ON public.users USING btree (platform_id);

--
-- Name: idx_vehicle_assets_family_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_vehicle_assets_family_group ON public.vehicle_assets USING btree (family_group);

--
-- Name: idx_workflows_is_active; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workflows_is_active ON public.workflows USING btree (is_active);

--
-- Name: idx_workflows_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_workflows_user_id ON public.workflows USING btree (user_id);

--
-- PostgreSQL database dump complete
--

-- +goose Down
-- The baseline is irreversible by design: to start over, drop and recreate the database.
