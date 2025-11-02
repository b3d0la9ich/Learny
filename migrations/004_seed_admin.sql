-- Включаем pgcrypto, чтобы сгенерировать bcrypt-хэш
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Админ по умолчанию (email и пароль можно потом поменять)
-- Пароль будет захеширован как bcrypt: crypt('пароль', gen_salt('bf', 12))
INSERT INTO users (email, pass_hash, role)
VALUES (
  'admin@learny.local',
  crypt('admin123', gen_salt('bf', 12)),
  'admin'
)
ON CONFLICT (email) DO NOTHING;
