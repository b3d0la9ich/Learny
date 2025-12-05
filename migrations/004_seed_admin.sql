CREATE EXTENSION IF NOT EXISTS pgcrypto;

INSERT INTO users (email, pass_hash, role_id)
VALUES (
  'admin@learny.local',
  crypt('admin123', gen_salt('bf', 12)),
  (SELECT id FROM roles WHERE name = 'admin')
)
ON CONFLICT (email) DO NOTHING;
