-- Migration 004: Email threading and reply metadata.

ALTER TABLE emails ADD COLUMN IF NOT EXISTS conversation_id VARCHAR;
ALTER TABLE emails ADD COLUMN IF NOT EXISTS root_email_id UUID REFERENCES emails(id) ON DELETE SET NULL;
ALTER TABLE emails ADD COLUMN IF NOT EXISTS parent_email_id UUID REFERENCES emails(id) ON DELETE SET NULL;
ALTER TABLE emails ADD COLUMN IF NOT EXISTS in_reply_to_message_id VARCHAR;
ALTER TABLE emails ADD COLUMN IF NOT EXISTS references_message_ids JSONB;

CREATE INDEX IF NOT EXISTS idx_emails_conversation_id ON emails(conversation_id);
CREATE INDEX IF NOT EXISTS idx_emails_root_email_id ON emails(root_email_id);
CREATE INDEX IF NOT EXISTS idx_emails_parent_email_id ON emails(parent_email_id);
CREATE INDEX IF NOT EXISTS idx_emails_in_reply_to_message_id ON emails(in_reply_to_message_id);
CREATE INDEX IF NOT EXISTS idx_emails_inbox_message_id ON emails(inbox_id, message_id);

ALTER TABLE sent_email_logs ADD COLUMN IF NOT EXISTS original_email_id UUID REFERENCES emails(id) ON DELETE SET NULL;
ALTER TABLE sent_email_logs ADD COLUMN IF NOT EXISTS parent_email_id UUID REFERENCES emails(id) ON DELETE SET NULL;
ALTER TABLE sent_email_logs ADD COLUMN IF NOT EXISTS conversation_id VARCHAR;
ALTER TABLE sent_email_logs ADD COLUMN IF NOT EXISTS mode VARCHAR;
ALTER TABLE sent_email_logs ADD COLUMN IF NOT EXISTS in_reply_to_message_id VARCHAR;
ALTER TABLE sent_email_logs ADD COLUMN IF NOT EXISTS references_message_ids JSONB;

CREATE INDEX IF NOT EXISTS idx_sent_email_logs_original_email_id ON sent_email_logs(original_email_id);
CREATE INDEX IF NOT EXISTS idx_sent_email_logs_parent_email_id ON sent_email_logs(parent_email_id);
CREATE INDEX IF NOT EXISTS idx_sent_email_logs_conversation_id ON sent_email_logs(conversation_id);
CREATE INDEX IF NOT EXISTS idx_sent_email_logs_mode ON sent_email_logs(mode);
CREATE INDEX IF NOT EXISTS idx_sent_email_logs_in_reply_to_message_id ON sent_email_logs(in_reply_to_message_id);
