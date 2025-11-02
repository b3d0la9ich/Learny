CREATE INDEX IF NOT EXISTS idx_attempts_user_quiz ON attempts(user_id, quiz_id);
CREATE INDEX IF NOT EXISTS idx_attempts_finished_at ON attempts(finished_at);
