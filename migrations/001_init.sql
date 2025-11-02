CREATE TABLE IF NOT EXISTS users (
  id        BIGSERIAL PRIMARY KEY,
  email     TEXT UNIQUE NOT NULL,
  pass_hash TEXT NOT NULL,
  role      TEXT NOT NULL DEFAULT 'student'
);

CREATE TABLE IF NOT EXISTS courses (
  id          BIGSERIAL PRIMARY KEY,
  title       TEXT NOT NULL,
  description TEXT
);

CREATE TABLE IF NOT EXISTS quizzes (
  id         BIGSERIAL PRIMARY KEY,
  course_id  BIGINT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
  title      TEXT NOT NULL,
  rules      JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE TABLE IF NOT EXISTS questions (
  id         BIGSERIAL PRIMARY KEY,
  course_id  BIGINT NOT NULL REFERENCES courses(id) ON DELETE CASCADE,
  topic      TEXT NOT NULL,
  difficulty INT  NOT NULL DEFAULT 3,
  q_type     TEXT NOT NULL CHECK (q_type IN ('single','multiple','numeric','text')),
  payload    JSONB NOT NULL
);

CREATE TABLE IF NOT EXISTS attempts (
  id          BIGSERIAL PRIMARY KEY,
  quiz_id     BIGINT NOT NULL REFERENCES quizzes(id) ON DELETE CASCADE,
  user_id     BIGINT NOT NULL REFERENCES users(id)   ON DELETE CASCADE,
  started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  finished_at TIMESTAMPTZ,
  total_score DOUBLE PRECISION
);

CREATE TABLE IF NOT EXISTS answers (
  id          BIGSERIAL PRIMARY KEY,
  attempt_id  BIGINT NOT NULL REFERENCES attempts(id)  ON DELETE CASCADE,
  question_id BIGINT NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
  answered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  is_correct  BOOLEAN,
  answer      JSONB
);

-- демо-курс
INSERT INTO courses (title, description)
VALUES ('Базовый курс', 'Демо-курс для стартовых квизов')
ON CONFLICT DO NOTHING;
