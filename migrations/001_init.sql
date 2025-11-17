-- ========== USERS ==========
CREATE TABLE IF NOT EXISTS users (
  id        BIGSERIAL PRIMARY KEY,
  email     TEXT UNIQUE NOT NULL,
  pass_hash TEXT NOT NULL,
  role      TEXT NOT NULL DEFAULT 'student'
);

-- ========== COURSES ==========
CREATE TABLE IF NOT EXISTS courses (
  id          BIGSERIAL PRIMARY KEY,
  title       TEXT NOT NULL,
  description TEXT
);

-- добавляем курсы с фиксированными id,
-- чтобы seed questions не падал с FK ошибками
INSERT INTO courses (id, title, description) VALUES
  (1, 'Базовый курс', 'Демо-курс для стартовых квизов'),
  (2, 'Алгоритмы и структуры данных', 'Автосид: вопросы из questions_all.json'),
  (3, 'Компьютерные сети', 'Автосид: вопросы из questions_all.json')
ON CONFLICT (id) DO NOTHING;

-- выравниваем sequence
SELECT setval('courses_id_seq', (SELECT COALESCE(MAX(id), 1) FROM courses));

-- ========== QUIZZES ==========
CREATE TABLE IF NOT EXISTS quizzes (
  id         BIGSERIAL PRIMARY KEY,
  course_id  BIGINT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
  title      TEXT NOT NULL,
  rules      JSONB NOT NULL DEFAULT '{}'::jsonb
);


-- ========== QUESTIONS ==========
CREATE TABLE IF NOT EXISTS questions (
  id           BIGSERIAL PRIMARY KEY,
  course_id    BIGINT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
  topic        TEXT NOT NULL,
  difficulty   INT  NOT NULL DEFAULT 3,
  qtype        TEXT NOT NULL CHECK (qtype IN ('single','multiple','numeric','text')),
  payload_json JSONB NOT NULL
);


-- ========== ATTEMPTS ==========
CREATE TABLE IF NOT EXISTS attempts (
  id          BIGSERIAL PRIMARY KEY,
  quiz_id     BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
  user_id     BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at TIMESTAMPTZ,
  total_score DOUBLE PRECISION
);

-- duration + overtime columns (002 migration)
ALTER TABLE attempts
  ADD COLUMN IF NOT EXISTS duration_sec INT,
  ADD COLUMN IF NOT EXISTS overtime BOOLEAN DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_attempts_user_quiz ON attempts (user_id, quiz_id);
CREATE INDEX IF NOT EXISTS idx_attempts_finished_at ON attempts (finished_at);


-- ========== ANSWERS ==========
CREATE TABLE IF NOT EXISTS answers (
  id          BIGSERIAL PRIMARY KEY,
  attempt_id  BIGINT NOT NULL REFERENCES attempts(id) ON DELETE CASCADE,
  question_id BIGINT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
  answered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  is_correct  BOOLEAN,
  answer      JSONB
);


-- ========== EXTENSIONS ==========
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ========== ADMIN (если нет) ==========
INSERT INTO users (email, pass_hash, role)
VALUES ('admin@example.com', crypt('admin123', gen_salt('bf')), 'admin')
ON CONFLICT (email) DO NOTHING;
