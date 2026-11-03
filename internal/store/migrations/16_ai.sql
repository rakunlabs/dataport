-- Conversations with the AI assistant of the workspace, kept until their
-- user deletes them. Messages are the provider's chat messages as JSON.
CREATE TABLE ${TABLE_PREFIX}ai_conversations (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}users(id) ON DELETE CASCADE,
    title TEXT NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);

CREATE INDEX ${TABLE_PREFIX}ai_conversations_user_idx ON ${TABLE_PREFIX}ai_conversations(user_id, updated_at);

CREATE TABLE ${TABLE_PREFIX}ai_messages (
    conversation_id TEXT NOT NULL REFERENCES ${TABLE_PREFIX}ai_conversations(id) ON DELETE CASCADE,
    seq INTEGER NOT NULL,
    payload TEXT NOT NULL,
    created_at BIGINT NOT NULL,
    PRIMARY KEY (conversation_id, seq)
);
