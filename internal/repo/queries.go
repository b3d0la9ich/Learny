package repo

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

/*** users ***/

type UserRow struct {
	ID       int64
	Email    string
	PassHash string
	Role     string
}

func (r *Repo) CreateUser(ctx context.Context, email, passHash string) (int64, error) {
	row := r.DB.QueryRowContext(ctx,
		`INSERT INTO users(email, pass_hash, role_id)
         VALUES ($1, $2, (SELECT id FROM roles WHERE name = 'student'))
         RETURNING id`,
		email, passHash,
	)
	var id int64
	return id, row.Scan(&id)
}


func (r *Repo) FindUserByEmail(ctx context.Context, email string) (*UserRow, error) {
	row := r.DB.QueryRowContext(ctx,
		`SELECT u.id, u.email, u.pass_hash, r.name AS role
         FROM users u
         JOIN roles r ON r.id = u.role_id
         WHERE u.email = $1`,
		email,
	)
	var u UserRow
	if err := row.Scan(&u.ID, &u.Email, &u.PassHash, &u.Role); err != nil {
		return nil, err
	}
	return &u, nil
}


func (r *Repo) UpdateUserPass(ctx context.Context, userID int64, newHash string) error {
	_, err := r.DB.ExecContext(ctx,
		`UPDATE users SET pass_hash=$2 WHERE id=$1`,
		userID, newHash,
	)
	return err
}

func (r *Repo) GetUserRole(ctx context.Context, userID int64) (string, error) {
	var role string
	err := r.DB.QueryRowContext(ctx,
		`SELECT r.name
         FROM users u
         JOIN roles r ON r.id = u.role_id
         WHERE u.id = $1`,
		userID,
	).Scan(&role)
	return role, err
}

func (r *Repo) ListUsers(ctx context.Context) ([]UserRow, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT u.id, u.email, u.pass_hash, r.name AS role
         FROM users u
         JOIN roles r ON r.id = u.role_id
         ORDER BY u.id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []UserRow
	for rows.Next() {
		var u UserRow
		if err := rows.Scan(&u.ID, &u.Email, &u.PassHash, &u.Role); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}


func (r *Repo) UpdateUserRole(ctx context.Context, userID int64, role string) error {
	switch role {
	case "student", "teacher", "admin":
	default:
		return fmt.Errorf("invalid role")
	}

	_, err := r.DB.ExecContext(ctx,
		`UPDATE users
         SET role_id = (SELECT id FROM roles WHERE name = $2)
         WHERE id = $1`,
		userID, role,
	)
	return err
}


/*** courses ***/

type CourseRow struct {
	ID          int64
	Title       string
	Description string
}

func (r *Repo) ListCourses(ctx context.Context) ([]CourseRow, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT id,title,description FROM courses ORDER BY id`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CourseRow
	for rows.Next() {
		var c CourseRow
		if err := rows.Scan(&c.ID, &c.Title, &c.Description); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repo) CreateCourse(ctx context.Context, title, description string) error {
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO courses(title, description) VALUES ($1,$2)`,
		title, description,
	)
	return err
}

func (r *Repo) UpdateCourse(ctx context.Context, id int64, title, description string) error {
	_, err := r.DB.ExecContext(ctx, `
		UPDATE courses
		   SET title       = COALESCE(NULLIF($2,''), title),
		       description = COALESCE(NULLIF($3,''), description)
		 WHERE id=$1
	`, id, title, description)
	return err
}

func (r *Repo) DeleteCourse(ctx context.Context, id int64) error {
	_, err := r.DB.ExecContext(ctx,
		`DELETE FROM courses WHERE id=$1`,
		id,
	)
	return err
}

/*** quizzes & questions ***/

type QuizRules struct {
	Count             int      `json:"count"`
	ByTopics          []string `json:"by_topics"`
	TimeLimitSec      int      `json:"time_limit_sec"`
	MaxAttempts       int      `json:"max_attempts"`
	RetakeCooldownSec int      `json:"retake_cooldown_sec"`
}

type QuizRow struct {
	ID    int64
	Title string
	Rules []byte
}

func (r *Repo) LoadQuizRules(ctx context.Context, quizID int64) (*QuizRules, string, error) {
	var rulesRaw []byte
	var title string

	err := r.DB.QueryRowContext(ctx,
		`SELECT rules, title FROM quizzes WHERE id=$1`, quizID,
	).Scan(&rulesRaw, &title)
	if err != nil {
		return nil, "", err
	}

	var q QuizRules
	if len(rulesRaw) > 0 {
		if err := json.Unmarshal(rulesRaw, &q); err != nil {
			return nil, "", err
		}
	}
	if q.Count == 0 {
		q.Count = 10
	}
	return &q, title, nil
}

func (r *Repo) ListQuizzesByCourse(ctx context.Context, courseID int64) ([]QuizRow, error) {
	rows, err := r.DB.QueryContext(ctx,
		`SELECT id, title, rules FROM quizzes WHERE course_id=$1 ORDER BY id`,
		courseID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []QuizRow
	for rows.Next() {
		var q QuizRow
		if err := rows.Scan(&q.ID, &q.Title, &q.Rules); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func (r *Repo) CreateQuiz(ctx context.Context, courseID int64, title string, rulesRaw []byte) error {
	var tmp map[string]any
	if err := json.Unmarshal(rulesRaw, &tmp); err != nil {
		return fmt.Errorf("invalid JSON in rules: %w", err)
	}
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO quizzes(course_id, title, rules) VALUES ($1,$2,$3)`,
		courseID, title, rulesRaw,
	)
	return err
}

func (r *Repo) DeleteQuiz(ctx context.Context, quizID int64) error {
	_, err := r.DB.ExecContext(ctx,
		`DELETE FROM quizzes WHERE id=$1`,
		quizID,
	)
	return err
}

/*** questions ***/

type QuestionRow struct {
	ID         int64
	CourseID   int64
	Topic      string
	QType      string
	Difficulty int
	Payload    json.RawMessage
}

// для JSON-импорта/автосида пригодится
func toPGTextArray(a []string) string {
	parts := make([]string, len(a))
	for i, s := range a {
		s = strings.ReplaceAll(s, `"`, `\"`)
		parts[i] = `"` + s + `"`
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func (r *Repo) PickQuestions(ctx context.Context, courseID int64, rules *QuizRules) ([]QuestionRow, error) {
	// берём количество из rules.Count, если 0 — 10
	total := rules.Count
	if total <= 0 {
		total = 10
	}

	const q = `
		SELECT id, topic, qtype, difficulty, payload_json
		FROM questions
		WHERE course_id = $1
		ORDER BY random()
		LIMIT $2
	`

	rows, err := r.DB.QueryContext(ctx, q, courseID, total)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []QuestionRow
	for rows.Next() {
		var qr QuestionRow
		if err := rows.Scan(&qr.ID, &qr.Topic, &qr.QType, &qr.Difficulty, &qr.Payload); err != nil {
			return nil, err
		}
		out = append(out, qr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Repo) FetchQuestionsByIDs(ctx context.Context, ids []int64) ([]QuestionRow, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	params := make([]interface{}, len(ids))
	placeholders := make([]string, len(ids))
	for i, id := range ids {
		params[i] = id
		placeholders[i] = "$" + strconv.Itoa(i+1)
	}

	query := `
		SELECT id, topic, qtype, difficulty, payload_json
		FROM questions
		WHERE id IN (` + strings.Join(placeholders, ",") + `)
	`

	rows, err := r.DB.QueryContext(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []QuestionRow
	for rows.Next() {
		var qr QuestionRow
		if err := rows.Scan(&qr.ID, &qr.Topic, &qr.QType, &qr.Difficulty, &qr.Payload); err != nil {
			return nil, err
		}
		out = append(out, qr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Repo) ListQuestions(ctx context.Context, courseID int64, topic, qtype string, limit int) ([]QuestionRow, error) {
	args := []interface{}{courseID}
	where := []string{"course_id = $1"}

	if topic != "" {
		args = append(args, "%"+topic+"%")
		where = append(where, "topic ILIKE $"+strconv.Itoa(len(args)))
	}
	if qtype != "" {
		args = append(args, qtype)
		where = append(where, "qtype = $"+strconv.Itoa(len(args)))
	}
	if limit <= 0 {
		limit = 100
	}
	args = append(args, limit)

	query := `
		SELECT id, course_id, topic, qtype, difficulty, payload_json
		FROM questions
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY id
		LIMIT $` + strconv.Itoa(len(args)) + `
	`

	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []QuestionRow
	for rows.Next() {
		var qr QuestionRow
		if err := rows.Scan(
			&qr.ID,
			&qr.CourseID,
			&qr.Topic,
			&qr.QType,
			&qr.Difficulty,
			&qr.Payload,
		); err != nil {
			return nil, err
		}
		out = append(out, qr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *Repo) GetQuestion(ctx context.Context, id int64) (*QuestionRow, error) {
	const q = `
		SELECT id, course_id, topic, qtype, difficulty, payload_json
		FROM questions
		WHERE id = $1
	`
	var row QuestionRow
	if err := r.DB.QueryRowContext(ctx, q, id).Scan(
		&row.ID,
		&row.CourseID,
		&row.Topic,
		&row.QType,
		&row.Difficulty,
		&row.Payload,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (r *Repo) UpdateQuestion(ctx context.Context, id int64, topic, qtype string, diff int, payload []byte) error {
	if qtype != "" {
		switch qtype {
		case "single", "multiple", "numeric", "text":
		default:
			return fmt.Errorf("invalid qtype")
		}
	}
	if diff != 0 && (diff < 1 || diff > 5) {
		return fmt.Errorf("invalid difficulty")
	}
	_, err := r.DB.ExecContext(ctx, `
		UPDATE questions
		   SET topic       = COALESCE(NULLIF($2,''), topic),
		       qtype       = COALESCE(NULLIF($3,''), qtype),
		       difficulty  = COALESCE(NULLIF($4,0), difficulty),
		       payload_json = COALESCE($5, payload_json)
		 WHERE id=$1
	`, id, topic, qtype, diff, payload)
	return err
}

/*** attempts & answers ***/

func (r *Repo) CreateAttempt(ctx context.Context, quizID, userID int64) (int64, error) {
	var id int64
	err := r.DB.QueryRowContext(ctx,
		`INSERT INTO attempts(quiz_id, user_id) VALUES ($1,$2) RETURNING id`,
		quizID, userID,
	).Scan(&id)
	return id, err
}

func (r *Repo) SaveAnswer(ctx context.Context, attemptID, questionID int64, isCorrect *bool, answer []byte) error {
	_, err := r.DB.ExecContext(ctx,
		`INSERT INTO answers(attempt_id, question_id, is_correct, answer) VALUES ($1,$2,$3,$4)`,
		attemptID, questionID, isCorrect, answer,
	)
	return err
}

func (r *Repo) SetAttemptResult(ctx context.Context, attemptID int64, finishedAt *time.Time, score *float64) error {
	_, err := r.DB.ExecContext(ctx,
		`UPDATE attempts SET finished_at=$2, total_score=$3 WHERE id=$1`,
		attemptID, finishedAt, score,
	)
	return err
}

func (r *Repo) SetAttemptTiming(ctx context.Context, attemptID int64, durationSec int, overtime bool) error {
	_, err := r.DB.ExecContext(ctx,
		`UPDATE attempts SET duration_sec=$2, overtime=$3 WHERE id=$1`,
		attemptID, durationSec, overtime,
	)
	return err
}

type AttemptRow struct {
	ID         int64
	UserEmail  string
	QuizTitle  string
	FinishedAt *time.Time
	Score      *float64
}

// ScoreVal — удобный геттер для вывода в шаблоне.
func (a AttemptRow) ScoreVal() float64 {
	if a.Score == nil {
		return 0
	}
	return *a.Score
}


func (r *Repo) ListAttemptsByCourse(ctx context.Context, courseID int64) ([]AttemptRow, error) {
	rows, err := r.DB.QueryContext(ctx, `
		SELECT a.id, u.email, qz.title, a.finished_at, a.total_score
		FROM attempts a
		JOIN users   u  ON u.id  = a.user_id
		JOIN quizzes qz ON qz.id = a.quiz_id
		WHERE qz.course_id=$1
		ORDER BY a.id DESC
	`, courseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AttemptRow
	for rows.Next() {
		var r0 AttemptRow
		if err := rows.Scan(&r0.ID, &r0.UserEmail, &r0.QuizTitle, &r0.FinishedAt, &r0.Score); err != nil {
			return nil, err
		}
		out = append(out, r0)
	}
	return out, rows.Err()
}

/* attempt detail */

type AttemptMeta struct {
	ID          int64
	UserEmail   string
	QuizTitle   string
	StartedAt   time.Time
	FinishedAt  *time.Time
	Score       *float64
	DurationSec *int
	Overtime    bool
}

type AnswerDetail struct {
	QuestionID int64
	Topic      string
	QType      string
	Payload    json.RawMessage
	IsCorrect  *bool
	Answer     json.RawMessage
}

func (r *Repo) GetAttemptWithAnswers(ctx context.Context, attemptID int64) (*AttemptMeta, []AnswerDetail, error) {
	var meta AttemptMeta
	err := r.DB.QueryRowContext(ctx, `
		SELECT a.id, u.email, qz.title,
		       a.started_at, a.finished_at, a.total_score,
		       a.duration_sec, a.overtime
		FROM attempts a
		JOIN users   u  ON u.id  = a.user_id
		JOIN quizzes qz ON qz.id = a.quiz_id
		WHERE a.id=$1
	`, attemptID).Scan(
		&meta.ID, &meta.UserEmail, &meta.QuizTitle,
		&meta.StartedAt, &meta.FinishedAt, &meta.Score,
		&meta.DurationSec, &meta.Overtime,
	)
	if err != nil {
		return nil, nil, err
	}

	rows, err := r.DB.QueryContext(ctx, `
		SELECT q.id, q.topic, q.qtype, q.payload_json, an.is_correct, an.answer
		FROM answers   an
		JOIN questions q ON q.id = an.question_id
		WHERE an.attempt_id=$1
		ORDER BY an.id
	`, attemptID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var out []AnswerDetail
	for rows.Next() {
		var d AnswerDetail
		if err := rows.Scan(
			&d.QuestionID,
			&d.Topic,
			&d.QType,
			&d.Payload,
			&d.IsCorrect,
			&d.Answer,
		); err != nil {
			return nil, nil, err
		}
		out = append(out, d)
	}
	return &meta, out, rows.Err()
}

/*** ограничители пересдачи ***/

func (r *Repo) TotalAttemptsByUserQuiz(ctx context.Context, userID, quizID int64) (int, error) {
	var n int
	err := r.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM attempts WHERE user_id=$1 AND quiz_id=$2`,
		userID, quizID,
	).Scan(&n)
	return n, err
}

func (r *Repo) AttemptsSinceByUserQuiz(ctx context.Context, userID, quizID int64, since time.Time) (int, error) {
	var n int
	err := r.DB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM attempts
		WHERE user_id=$1 AND quiz_id=$2 AND started_at >= $3
	`, userID, quizID, since).Scan(&n)
	return n, err
}

/*** экспорт ***/

type AttemptExportRow struct {
	AttemptID int64
	UserEmail string
	CourseID  int64
	QuizID    int64
	QuizTitle string
	StartedAt time.Time
	FinishedAt *time.Time
	Score     *float64
	Duration  *int
	Overtime  bool
}

func (r *Repo) ExportAttempts(ctx context.Context, courseID *int64, quizID *int64) ([]AttemptExportRow, error) {
	sb := strings.Builder{}
	args := []any{}
	i := 1

	sb.WriteString(`
		SELECT a.id, u.email, q.course_id, q.id, q.title,
		       a.started_at, a.finished_at, a.total_score,
		       a.duration_sec, a.overtime
		FROM attempts a
		JOIN users   u ON u.id = a.user_id
		JOIN quizzes q ON q.id = a.quiz_id
	`)
	where := []string{}
	if courseID != nil {
		where = append(where, fmt.Sprintf("q.course_id=$%d", i))
		args = append(args, *courseID)
		i++
	}
	if quizID != nil {
		where = append(where, fmt.Sprintf("q.id=$%d", i))
		args = append(args, *quizID)
		i++
	}
	if len(where) > 0 {
		sb.WriteString(" WHERE " + strings.Join(where, " AND "))
	}
	sb.WriteString(" ORDER BY a.id DESC")

	rows, err := r.DB.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AttemptExportRow
	for rows.Next() {
		var r0 AttemptExportRow
		if err := rows.Scan(
			&r0.AttemptID, &r0.UserEmail,
			&r0.CourseID, &r0.QuizID, &r0.QuizTitle,
			&r0.StartedAt, &r0.FinishedAt, &r0.Score,
			&r0.Duration, &r0.Overtime,
		); err != nil {
			return nil, err
		}
		out = append(out, r0)
	}
	return out, rows.Err()
}

/*** статистика по темам ***/

type TopicStat struct {
	Topic   string
	Total   int
	Correct int
}

func (r *Repo) TopicStatsByUser(ctx context.Context, userID, courseID int64) ([]TopicStat, error) {
	rows, err := r.DB.QueryContext(ctx, `
		SELECT q.topic,
		       COUNT(*) AS total,
		       SUM(CASE WHEN a.is_correct THEN 1 ELSE 0 END) AS correct
		FROM answers a
		JOIN attempts t ON t.id = a.attempt_id
		JOIN questions q ON q.id = a.question_id
		JOIN quizzes   z ON z.id = t.quiz_id
		WHERE t.user_id=$1 AND z.course_id=$2
		GROUP BY q.topic
		ORDER BY q.topic
	`, userID, courseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TopicStat
	for rows.Next() {
		var s TopicStat
		if err := rows.Scan(&s.Topic, &s.Total, &s.Correct); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

type TopicDetailRow struct {
	QID    int64
	When   time.Time
	Correct *bool
}

func (r *Repo) TopicDetail(ctx context.Context, userID, courseID int64, topic string) ([]TopicDetailRow, error) {
	rows, err := r.DB.QueryContext(ctx, `
		SELECT q.id, a.answered_at, a.is_correct
		FROM answers a
		JOIN attempts t ON t.id = a.attempt_id
		JOIN questions q ON q.id = a.question_id
		JOIN quizzes   z ON z.id = t.quiz_id
		WHERE t.user_id=$1 AND z.course_id=$2 AND q.topic=$3
		ORDER BY a.answered_at DESC
		LIMIT 200
	`, userID, courseID, topic)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []TopicDetailRow
	for rows.Next() {
		var r0 TopicDetailRow
		if err := rows.Scan(&r0.QID, &r0.When, &r0.Correct); err != nil {
			return nil, err
		}
		out = append(out, r0)
	}
	return out, rows.Err()
}

/*** importers ***/

func (r *Repo) ImportQuestionsCSV(ctx context.Context, reader *csv.Reader, courseID int64) (int, error) {
	count := 0
	for {
		rec, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return count, err
		}
		if len(rec) < 6 {
			return count, fmt.Errorf("invalid record length: %v", rec)
		}

		topic := strings.TrimSpace(rec[0])
		qtype := strings.TrimSpace(rec[1])
		qtext := strings.TrimSpace(rec[2])
		choicesRaw := ""
		correctRaw := ""
		if len(rec) > 3 {
			choicesRaw = strings.TrimSpace(rec[3])
		}
		if len(rec) > 4 {
			correctRaw = strings.TrimSpace(rec[4])
		}
		diffStr := strings.TrimSpace(rec[5])

		diff := 3
		if v, err := strconv.Atoi(diffStr); err == nil {
			diff = v
		}

		var payload map[string]any
		switch qtype {
		case "single":
			choices := splitComma(choicesRaw)
			corrIdx, _ := strconv.Atoi(strings.TrimSpace(correctRaw))
			payload = map[string]any{
				"text":    qtext,
				"choices": choices,
				"correct": []int{corrIdx},
			}
		case "multiple":
			choices := splitComma(choicesRaw)
			var corr []int
			for _, p := range splitComma(correctRaw) {
				if i, err := strconv.Atoi(p); err == nil {
					corr = append(corr, i)
				}
			}
			payload = map[string]any{
				"text":    qtext,
				"choices": choices,
				"correct": corr,
			}
		case "numeric":
			val, _ := strconv.ParseFloat(strings.ReplaceAll(correctRaw, ",", "."), 64)
			payload = map[string]any{
				"text":          qtext,
				"correct_value": val,
			}
		case "text":
			payload = map[string]any{
				"text":   qtext,
				"accept": splitComma(correctRaw),
			}
		default:
			return count, fmt.Errorf("unsupported qtype: %s", qtype)
		}
		raw, _ := json.Marshal(payload)
		if _, err := r.DB.ExecContext(ctx,
			`INSERT INTO questions(course_id, topic, difficulty, qtype, payload_json)
			 VALUES ($1,$2,$3,$4,$5)`,
			courseID, topic, diff, qtype, raw,
		); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// JSON массив объектов: { "topic","qtype","difficulty","payload_json":{...} }
func (r *Repo) ImportQuestionsJSON(ctx context.Context, raw []byte, courseID int64) (int, error) {
	var items []struct {
		Topic      string          `json:"topic"`
		QType      string          `json:"qtype"`
		Difficulty int             `json:"difficulty"`
		Payload    json.RawMessage `json:"payload_json"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return 0, fmt.Errorf("invalid JSON: %w", err)
	}
	n := 0
	for _, it := range items {
		if it.Topic == "" || it.QType == "" || len(it.Payload) == 0 {
			return n, fmt.Errorf("missing fields in item #%d", n+1)
		}
		if it.Difficulty == 0 {
			it.Difficulty = 3
		}
		if _, err := r.DB.ExecContext(ctx,
			`INSERT INTO questions(course_id, topic, difficulty, qtype, payload_json)
			 VALUES ($1,$2,$3,$4,$5)`,
			courseID, it.Topic, it.Difficulty, it.QType, []byte(it.Payload),
		); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

/*** helpers ***/

func splitComma(s string) []string {
	if s == "" {
		return nil
	}
	ps := strings.Split(s, ",")
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ===== Логи пользователя (для /admin/logs) =====

type UserLogSummary struct {
	UserEmail string
	Attempts  int
	LastAt    *time.Time
	Correct   int
	Wrong     int
}

type UserLogRow struct {
	AttemptID int64
	When      time.Time
	Topic     string
	QType     string
	IsCorrect *bool
}

// UserLogs возвращает сводку и последние ответы пользователя
func (r *Repo) UserLogs(ctx context.Context, userID int64) (*UserLogSummary, []UserLogRow, error) {
	// Сводка
	var sum UserLogSummary
	err := r.DB.QueryRowContext(ctx, `
		SELECT u.email,
		       COALESCE(COUNT(DISTINCT t.id), 0) AS attempts,
		       MAX(t.finished_at)                AS last_at,
		       COALESCE(SUM(CASE WHEN a.is_correct IS TRUE  THEN 1 ELSE 0 END), 0) AS correct,
		       COALESCE(SUM(CASE WHEN a.is_correct IS FALSE THEN 1 ELSE 0 END), 0) AS wrong
		FROM users u
		LEFT JOIN attempts t ON t.user_id = u.id
		LEFT JOIN answers  a ON a.attempt_id = t.id
		WHERE u.id = $1
		GROUP BY u.email
	`, userID).Scan(&sum.UserEmail, &sum.Attempts, &sum.LastAt, &sum.Correct, &sum.Wrong)
	if err != nil {
		if err == sql.ErrNoRows {
			row := r.DB.QueryRowContext(ctx, `SELECT email FROM users WHERE id=$1`, userID)
			if e := row.Scan(&sum.UserEmail); e != nil {
				return nil, nil, e
			}
			sum.Attempts, sum.Correct, sum.Wrong = 0, 0, 0
		} else {
			return nil, nil, err
		}
	}

	// Последние ответы
	rows, err := r.DB.QueryContext(ctx, `
		SELECT t.id       AS attempt_id,
		       a.answered_at,
		       q.topic,
		       q.qtype,
		       a.is_correct
		FROM answers a
		JOIN attempts t ON t.id = a.attempt_id
		JOIN questions q ON q.id = a.question_id
		WHERE t.user_id = $1
		ORDER BY a.answered_at DESC
		LIMIT 200
	`, userID)
	if err != nil {
		return &sum, nil, err
	}
	defer rows.Close()

	var out []UserLogRow
	for rows.Next() {
		var r0 UserLogRow
		if err := rows.Scan(&r0.AttemptID, &r0.When, &r0.Topic, &r0.QType, &r0.IsCorrect); err != nil {
			return &sum, nil, err
		}
		out = append(out, r0)
	}
	if err := rows.Err(); err != nil {
		return &sum, nil, err
	}
	return &sum, out, nil
}
