package httpx

import (
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"html/template"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	a "learny/internal/auth"
	"learny/internal/repo"
	"learny/internal/util"
)

type Server struct {
	DB   *sql.DB
	Repo *repo.Repo
	T    *template.Template

	loginLimiter sync.Map // IP -> *loginBucket
}

func (s *Server) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/register", s.handleRegister)
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)

	mux.Handle("/settings/password", RequireAuth(http.HandlerFunc(s.handlePasswordChange)))

	mux.Handle("/courses", RequireAuth(http.HandlerFunc(s.handleCourses)))
	mux.Handle("/quiz/start", RequireAuth(http.HandlerFunc(s.handleQuizStart)))
	mux.Handle("/quiz/finish", RequireAuth(http.HandlerFunc(s.handleQuizFinish)))

	mux.Handle("/topics", RequireAuth(http.HandlerFunc(s.handleTopics)))
	mux.Handle("/topic", RequireAuth(http.HandlerFunc(s.handleTopicProfile)))

	// Админка
	mux.Handle("/admin/questions", RequireRole(s.Repo, "teacher", "admin")(http.HandlerFunc(s.handleAdminQuestionsList)))
	mux.Handle("/admin/questions/edit", RequireRole(s.Repo, "teacher", "admin")(http.HandlerFunc(s.handleAdminQuestionEdit)))
	mux.Handle("/admin/questions/upload", RequireRole(s.Repo, "teacher", "admin")(http.HandlerFunc(s.handleAdminUploadGetPost)))
	mux.Handle("/admin/questions/import-json", RequireRole(s.Repo, "teacher", "admin")(http.HandlerFunc(s.handleAdminUploadJSON)))

	mux.Handle("/admin/users", RequireRole(s.Repo, "admin")(http.HandlerFunc(s.handleAdminUsers)))
	mux.Handle("/admin/courses", RequireRole(s.Repo, "teacher", "admin")(http.HandlerFunc(s.handleAdminCourses)))
	mux.Handle("/admin/quizzes", RequireRole(s.Repo, "teacher", "admin")(http.HandlerFunc(s.handleAdminQuizzes)))
	mux.Handle("/admin/results", RequireRole(s.Repo, "teacher", "admin")(http.HandlerFunc(s.handleAdminResults)))
	mux.Handle("/admin/results/export", RequireRole(s.Repo, "teacher", "admin")(http.HandlerFunc(s.handleAdminResultsExport)))
	mux.Handle("/admin/attempt", RequireRole(s.Repo, "teacher", "admin")(http.HandlerFunc(s.handleAdminAttemptDetail)))
	mux.Handle("/admin/logs", RequireRole(s.Repo, "teacher", "admin")(http.HandlerFunc(s.handleAdminLogsByUser)))
}

/* ---------- универсальный рендер с подбором имени шаблона ---------- */

// render: поднимает только base + конкретную страницу, чтобы не было конфликтов define.
func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	// прокинем признак авторизации и роль
	if uid, ok := a.CurrentUserID(r); ok {
		data["Authed"] = true
		data["UserID"] = uid
		if role, err := s.Repo.GetUserRole(r.Context(), uid); err == nil {
			data["UserRole"] = role
			data["IsAdmin"] = role == "admin"
			data["IsTeacher"] = role == "teacher" || role == "admin"
		}
	} else {
		data["Authed"] = false
	}

	// где лежат шаблоны
	root := "web/templates"

	// кандидаты для base
	baseCandidates := []string{"base.tmpl.html", "base.html"}
	var basePath string
	for _, f := range baseCandidates {
		p := filepath.Join(root, f)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			basePath = p
			break
		}
	}
	if basePath == "" {
		http.Error(w, "template error: base template not found", http.StatusInternalServerError)
		return
	}

	// кандидаты для страницы
	pageCandidates := []string{
		name,
		name + ".tmpl.html",
		name + ".html",
		"page_" + name,
		"page_" + name + ".tmpl.html",
	}
	var pagePath, pageTplName string
	for _, f := range pageCandidates {
		p := filepath.Join(root, f)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			pagePath = p
			pageTplName = filepath.Base(p) // имя для ExecuteTemplate
			break
		}
	}
	if pagePath == "" {
		// если нет отдельного файла страницы — попробуем выполнить то, что уже распарсили глобально
		if t := s.T.Lookup(name); t != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if err := t.Execute(w, data); err != nil {
				http.Error(w, "template exec error: "+err.Error(), http.StatusInternalServerError)
			}
			return
		}
		if t := s.T.Lookup(name + ".tmpl.html"); t != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if err := t.Execute(w, data); err != nil {
				http.Error(w, "template exec error: "+err.Error(), http.StatusInternalServerError)
			}
			return
		}
		http.Error(w, "template not found for '"+name+"'", http.StatusInternalServerError)
		return
	}

	// Парсим ТОЛЬКО base + выбранную страницу (никаких других файлов)
	t, err := template.New("").ParseFiles(basePath, pagePath)
	if err != nil {
		http.Error(w, "template parse error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Выполняем именно шаблон с именем файла страницы (внутри он сам подключит base)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := t.ExecuteTemplate(w, pageTplName, data); err != nil {
		http.Error(w, "template exec error: "+err.Error(), http.StatusInternalServerError)
		return
	}
}

/* ------------------------------ страницы ------------------------------ */

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.CurrentUserID(r); ok {
		http.Redirect(w, r, "/courses", http.StatusFound)
		return
	}
	s.render(w, r, "landing", nil)
}

/* ===== Регистрация/логин/выход + rate limit ===== */

type loginBucket struct {
	count int
	start time.Time
}

func clientIP(r *http.Request) string {
	ip := r.Header.Get("X-Forwarded-For")
	if ip != "" {
		if p := strings.Split(ip, ","); len(p) > 0 {
			return strings.TrimSpace(p[0])
		}
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host == "" {
		host = r.RemoteAddr
	}
	return host
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.render(w, r, "register", nil)
	case http.MethodPost:
		email := strings.TrimSpace(r.FormValue("email"))
		pw := r.FormValue("password")
		if len(email) == 0 || len(pw) < 8 {
			s.render(w, r, "register", map[string]any{"Error": "Укажите валидный email и пароль ≥ 8 символов"})
			return
		}
		hash, err := util.HashPassword(pw)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		if _, err := s.Repo.CreateUser(r.Context(), email, hash); err != nil {
			s.render(w, r, "register", map[string]any{"Error": "Пользователь с таким email уже существует"})
			return
		}
		u, _ := s.Repo.FindUserByEmail(r.Context(), email)
		a.SetSession(w, u.ID)
		http.Redirect(w, r, "/courses", http.StatusFound)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.render(w, r, "login", nil)
	case http.MethodPost:
		ip := clientIP(r)
		now := time.Now()
		val, _ := s.loginLimiter.LoadOrStore(ip, &loginBucket{count: 0, start: now})
		b := val.(*loginBucket)
		if now.Sub(b.start) > 15*time.Minute {
			b.start = now
			b.count = 0
		}
		if b.count >= 5 {
			s.render(w, r, "login", map[string]any{
				"Error": "Слишком много попыток. Подождите 15 минут и попробуйте снова.",
			})
			return
		}

		email := strings.TrimSpace(r.FormValue("email"))
		pw := r.FormValue("password")
		u, err := s.Repo.FindUserByEmail(r.Context(), email)
		if err != nil || !util.CheckPassword(u.PassHash, pw) {
			b.count++
			s.render(w, r, "login", map[string]any{"Error": "Неверный логин или пароль"})
			return
		}
		b.count = 0
		b.start = now
		a.SetSession(w, u.ID)
		http.Redirect(w, r, "/courses", http.StatusFound)
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	a.ClearSession(w)
	http.Redirect(w, r, "/login", http.StatusFound)
}

/* ===== Смена пароля ===== */

func (s *Server) handlePasswordChange(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.render(w, r, "settings_password", nil)
	case http.MethodPost:
		uid, _ := a.CurrentUserID(r)
		cur := r.FormValue("current")
		newp := r.FormValue("new")
		rep := r.FormValue("new2")
		if len(newp) < 8 || newp != rep {
			s.render(w, r, "settings_password",
				map[string]any{"Error": "Пароль должен быть ≥ 8 символов, и поля нового пароля должны совпадать"})
			return
		}
		var passHash string
		if err := s.DB.QueryRowContext(r.Context(), `SELECT pass_hash FROM users WHERE id=$1`, uid).Scan(&passHash); err != nil {
			http.Error(w, "user not found", 404)
			return
		}
		if !util.CheckPassword(passHash, cur) {
			s.render(w, r, "settings_password", map[string]any{"Error": "Текущий пароль неверен"})
			return
		}
		hash, _ := util.HashPassword(newp)
		if err := s.Repo.UpdateUserPass(r.Context(), uid, hash); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		s.render(w, r, "message", map[string]any{"Title": "Готово", "Message": "Пароль изменён."})
	}
}

/* ===== Курсы/квизы ===== */

func (s *Server) handleCourses(w http.ResponseWriter, r *http.Request) {
	cs, _ := s.Repo.ListCourses(r.Context())
	uid, _ := a.CurrentUserID(r)
	role, _ := s.Repo.GetUserRole(r.Context(), uid)
	qmap := map[int64][]repo.QuizRow{}
	for _, c := range cs {
		qs, _ := s.Repo.ListQuizzesByCourse(r.Context(), c.ID)
		qmap[c.ID] = qs
	}
	s.render(w, r, "courses", map[string]any{"Courses": cs, "Role": role, "QMap": qmap})
}

func (s *Server) handleQuizStart(w http.ResponseWriter, r *http.Request) {
	uid, _ := a.CurrentUserID(r)

	courseID := int64(1)
	if v := r.URL.Query().Get("course_id"); v != "" {
		if x, err := strconv.ParseInt(v, 10, 64); err == nil {
			courseID = x
		}
	}
	quizID := int64(1)
	if v := r.URL.Query().Get("quiz_id"); v != "" {
		if x, err := strconv.ParseInt(v, 10, 64); err == nil {
			quizID = x
		}
	}

	rules, title, err := s.Repo.LoadQuizRules(r.Context(), quizID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// лимиты
	if rules.MaxAttempts > 0 {
		total, _ := s.Repo.TotalAttemptsByUserQuiz(r.Context(), uid, quizID)
		if total >= rules.MaxAttempts {
			s.render(w, r, "message", map[string]any{
				"Title":   "Лимит попыток исчерпан",
				"Message": "Для этого квиза исчерпано максимальное число попыток.",
			})
			return
		}
	}
	if rules.RetakeCooldownSec > 0 {
		since := time.Now().Add(-time.Duration(rules.RetakeCooldownSec) * time.Second)
		count, _ := s.Repo.AttemptsSinceByUserQuiz(r.Context(), uid, quizID, since)
		if count > 0 {
			s.render(w, r, "message", map[string]any{
				"Title":   "Слишком рано для пересдачи",
				"Message": "Подождите перед новой попыткой согласно правилам квиза.",
			})
			return
		}
	}

	qs, err := s.Repo.PickQuestions(r.Context(), courseID, rules)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	attemptID, err := s.Repo.CreateAttempt(r.Context(), quizID, uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// обёртка для красивой нумерации 1..N
	type quizQuestionView struct {
		Ord        int
		ID         int64
		Topic      string
		QType      string
		Difficulty int
		Payload    json.RawMessage
	}

	vqs := make([]quizQuestionView, 0, len(qs))
	for i, q := range qs {
		vqs = append(vqs, quizQuestionView{
			Ord:        i + 1,
			ID:         q.ID,
			Topic:      q.Topic,
			QType:      q.QType,
			Difficulty: q.Difficulty,
			Payload:    q.Payload,
		})
	}

	var tl int
	if rules.TimeLimitSec > 0 {
		tl = rules.TimeLimitSec
	}

	s.render(w, r, "quiz", map[string]any{
		"Title":        title,
		"AttemptID":    attemptID,
		"Questions":    vqs,
		"TimeLimitSec": tl,
		"QuizID":       quizID,
	})
}

func (s *Server) handleQuizFinish(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	attemptID, _ := strconv.ParseInt(r.FormValue("attempt_id"), 10, 64)
	quizID, _ := strconv.ParseInt(r.FormValue("quiz_id"), 10, 64)
	clientElapsed, _ := strconv.ParseInt(r.FormValue("elapsed_sec"), 10, 64)

	values := map[int64][]string{}
	var qIDs []int64
	for key, vals := range r.PostForm {
		if !strings.HasPrefix(key, "q_") {
			continue
		}
		idStr := strings.TrimPrefix(key, "q_")
		qid, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			continue
		}
		qIDs = append(qIDs, qid)
		values[qid] = vals
	}
	qs, err := s.Repo.FetchQuestionsByIDs(r.Context(), qIDs)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	var rules *repo.QuizRules
	if quizID > 0 {
		rules, _, _ = s.Repo.LoadQuizRules(r.Context(), quizID)
	}

	var correctCount int
	for _, q := range qs {
		rawVals := values[q.ID]
		var isCorrect *bool
		var ansJSON []byte

		switch q.QType {
		case "single":
			var p struct {
				Text    string
				Choices []string
				Correct []int
			}
			_ = json.Unmarshal(q.Payload, &p)
			chosenIdx, _ := strconv.Atoi(firstOrEmpty(rawVals))
			ok := len(p.Correct) > 0 && chosenIdx == p.Correct[0]
			isCorrect = &ok
			if ok {
				correctCount++
			}
			ansJSON, _ = json.Marshal(map[string]any{"type": "single", "chosen": chosenIdx})

		case "multiple":
			var p struct {
				Text    string
				Choices []string
				Correct []int
			}
			_ = json.Unmarshal(q.Payload, &p)
			var chosen []int
			for _, sv := range rawVals {
				if i, err := strconv.Atoi(sv); err == nil {
					chosen = append(chosen, i)
				}
			}
			ok := setEq(intSliceToSet(chosen), intSliceToSet(p.Correct))
			isCorrect = &ok
			if ok {
				correctCount++
			}
			ansJSON, _ = json.Marshal(map[string]any{"type": "multiple", "chosen": chosen})

		case "numeric":
			var p struct {
				Text         string
				CorrectValue float64
			}
			_ = json.Unmarshal(q.Payload, &p)
			val, _ := strconv.ParseFloat(firstOrEmpty(rawVals), 64)
			ok := abs(val-p.CorrectValue) < 1e-9
			isCorrect = &ok
			if ok {
				correctCount++
			}
			ansJSON, _ = json.Marshal(map[string]any{"type": "numeric", "value": val})

		case "text":
			var p struct {
				Text   string
				Accept []string
			}
			_ = json.Unmarshal(q.Payload, &p)
			ans := strings.TrimSpace(firstOrEmpty(rawVals))
			ok := containsCI(p.Accept, ans)
			isCorrect = &ok
			if ok {
				correctCount++
			}
			ansJSON, _ = json.Marshal(map[string]any{"type": "text", "value": ans})
		}
		if err := s.Repo.SaveAnswer(r.Context(), attemptID, q.ID, isCorrect, ansJSON); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
	}

	score := float64(correctCount)
	now := time.Now()
	if err := s.Repo.SetAttemptResult(r.Context(), attemptID, &now, &score); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	dur := int(clientElapsed)
	overtime := false
	if rules != nil && rules.TimeLimitSec > 0 && dur > rules.TimeLimitSec {
		overtime = true
	}
	_ = s.Repo.SetAttemptTiming(r.Context(), attemptID, dur, overtime)

	s.render(w, r, "result", map[string]any{"AttemptID": attemptID, "Score": score})
}

func (s *Server) handleTopics(w http.ResponseWriter, r *http.Request) {
	uid, _ := a.CurrentUserID(r)
	courseID := int64(1)
	stats, _ := s.Repo.TopicStatsByUser(r.Context(), uid, courseID)

	type Row struct {
		Topic   string
		Total   int
		Correct int
		Percent int
	}
	var rows []Row
	for _, st := range stats {
		p := 0
		if st.Total > 0 {
			p = int((float64(st.Correct)/float64(st.Total))*100.0 + 0.5)
		}
		rows = append(rows, Row{Topic: st.Topic, Total: st.Total, Correct: st.Correct, Percent: p})
	}
	s.render(w, r, "topics", map[string]any{"Rows": rows})
}

func (s *Server) handleTopicProfile(w http.ResponseWriter, r *http.Request) {
	uid, _ := a.CurrentUserID(r)
	courseID := int64(1)

	topic := strings.TrimSpace(r.URL.Query().Get("name"))
	if topic == "" {
		http.Redirect(w, r, "/topics", http.StatusFound)
		return
	}

	detail, err := s.Repo.TopicDetail(r.Context(), uid, courseID, topic)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	type Row struct {
		Ord     int
		QID     int64
		WhenStr string
		Status  string
	}

	rows := make([]Row, 0, len(detail))
	for i, d := range detail {

		// красивое время
		when := d.When.In(time.Local).Format("02.01.2006 15:04")

		// строка статуса
		st := "—"
		if d.Correct != nil {
			if *d.Correct {
				st = "✔ Верно"
			} else {
				st = "✘ Неверно"
			}
		}

		rows = append(rows, Row{
			Ord:     i + 1,
			QID:     d.QID,
			WhenStr: when,
			Status:  st,
		})
	}

	s.render(w, r, "topic", map[string]any{
		"Topic": topic,
		"Rows":  rows,
	})
}

/* ===== Импорт CSV/JSON ===== */

func (s *Server) handleAdminUploadGetPost(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cs, _ := s.Repo.ListCourses(r.Context())
		s.render(w, r, "admin_upload", map[string]any{"Courses": cs})
	case http.MethodPost:
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "form too large", 400)
			return
		}
		cidStr := r.FormValue("course_id")
		if cidStr == "" {
			http.Error(w, "course_id required", 400)
			return
		}
		courseID, _ := strconv.ParseInt(cidStr, 10, 64)

		file, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "file required", 400)
			return
		}
		defer file.Close()

		reader := csv.NewReader(file)
		reader.Comma = ';'
		reader.FieldsPerRecord = -1

		count, err := s.Repo.ImportQuestionsCSV(r.Context(), reader, courseID)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		cs, _ := s.Repo.ListCourses(r.Context())
		s.render(w, r, "admin_upload",
			map[string]any{"OK": true, "Count": count, "Courses": cs, "Selected": courseID})
	}
}

func (s *Server) handleAdminUploadJSON(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cs, _ := s.Repo.ListCourses(r.Context())
		s.render(w, r, "admin_upload_json", map[string]any{"Courses": cs})
	case http.MethodPost:
		cidStr := r.FormValue("course_id")
		if cidStr == "" {
			http.Error(w, "course_id required", 400)
			return
		}
		courseID, _ := strconv.ParseInt(cidStr, 10, 64)

		var raw []byte
		file, _, err := r.FormFile("file")
		if err == nil {
			defer file.Close()
			raw, _ = io.ReadAll(file)
		} else {
			raw = []byte(r.FormValue("json"))
		}
		if len(raw) == 0 {
			http.Error(w, "empty JSON", 400)
			return
		}

		n, err := s.Repo.ImportQuestionsJSON(r.Context(), raw, courseID)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}

		cs, _ := s.Repo.ListCourses(r.Context())
		s.render(w, r, "admin_upload_json",
			map[string]any{"OK": true, "Count": n, "Courses": cs, "Selected": courseID})
	}
}

/* ===== Админ: пользователи/курсы/квизы/результаты ===== */

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		users, _ := s.Repo.ListUsers(r.Context())
		s.render(w, r, "admin_users", map[string]any{"Users": users})
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		id, _ := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
		role := strings.TrimSpace(r.FormValue("role"))
		if role == "" {
			http.Error(w, "role required", 400)
			return
		}
		if err := s.Repo.UpdateUserRole(r.Context(), id, role); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
	}
}

func (s *Server) handleAdminCourses(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cs, _ := s.Repo.ListCourses(r.Context())
		s.render(w, r, "admin_courses", map[string]any{"Courses": cs})
	case http.MethodPost:
		action := r.FormValue("action")
		switch action {
		case "create":
			title := strings.TrimSpace(r.FormValue("title"))
			desc := strings.TrimSpace(r.FormValue("description"))
			if title == "" {
				http.Error(w, "title required", 400)
				return
			}
			if err := s.Repo.CreateCourse(r.Context(), title, desc); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		case "update":
			id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
			title := strings.TrimSpace(r.FormValue("title"))
			desc := strings.TrimSpace(r.FormValue("description"))
			if err := s.Repo.UpdateCourse(r.Context(), id, title, desc); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		case "delete":
			id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
			if err := s.Repo.DeleteCourse(r.Context(), id); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
		}
		http.Redirect(w, r, "/admin/courses", http.StatusSeeOther)
	}
}

func (s *Server) handleAdminQuizzes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cid := int64(1)
		if v := r.URL.Query().Get("course_id"); v != "" {
			if x, err := strconv.ParseInt(v, 10, 64); err == nil {
				cid = x
			}
		}
		cs, _ := s.Repo.ListCourses(r.Context())
		qs, _ := s.Repo.ListQuizzesByCourse(r.Context(), cid)
		s.render(w, r, "admin_quizzes", map[string]any{"Courses": cs, "Selected": cid, "Quizzes": qs})
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		action := r.FormValue("action")
		switch action {
		case "create":
			cid, _ := strconv.ParseInt(r.FormValue("course_id"), 10, 64)
			title := strings.TrimSpace(r.FormValue("title"))
			rules := strings.TrimSpace(r.FormValue("rules_json"))
			if title == "" || rules == "" {
				http.Error(w, "title and rules_json required", 400)
				return
			}
			if err := s.Repo.CreateQuiz(r.Context(), cid, title, []byte(rules)); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			http.Redirect(w, r, "/admin/quizzes?course_id="+strconv.FormatInt(cid, 10), http.StatusSeeOther)
		case "delete":
			qid, _ := strconv.ParseInt(r.FormValue("quiz_id"), 10, 64)
			cid, _ := strconv.ParseInt(r.FormValue("course_id"), 10, 64)
			if err := s.Repo.DeleteQuiz(r.Context(), qid); err != nil {
				http.Error(w, err.Error(), 400)
				return
			}
			http.Redirect(w, r, "/admin/quizzes?course_id="+strconv.FormatInt(cid, 10), http.StatusSeeOther)
		}
	}
}

type adminResultAttempt struct {
	Ord       int
	ID        int64
	UserEmail string
	QuizTitle string

	WhenStr  string
	HasScore bool
	ScoreVal float64
}

type adminResultsPage struct {
	Courses  []repo.CourseRow
	Selected int64
	Attempts []adminResultAttempt
}

func (s *Server) handleAdminResults(w http.ResponseWriter, r *http.Request) {
	cid := int64(1)
	if v := r.URL.Query().Get("course_id"); v != "" {
		if x, err := strconv.ParseInt(v, 10, 64); err == nil {
			cid = x
		}
	}

	cs, err := s.Repo.ListCourses(r.Context())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	rows, err := s.Repo.ListAttemptsByCourse(r.Context(), cid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	view := make([]adminResultAttempt, 0, len(rows))
	for i, a := range rows {
		var whenStr string
		if a.FinishedAt != nil {
			whenStr = a.FinishedAt.In(time.Local).Format("02.01.2006 15:04:05")
		} else {
			whenStr = ""
		}

		hasScore := a.Score != nil
		val := 0.0
		if a.Score != nil {
			val = *a.Score
		}

		view = append(view, adminResultAttempt{
			Ord:       i + 1,
			ID:        a.ID,
			UserEmail: a.UserEmail,
			QuizTitle: a.QuizTitle,
			WhenStr:   whenStr,
			HasScore:  hasScore,
			ScoreVal:  val,
		})
	}

	page := adminResultsPage{
		Courses:  cs,
		Selected: cid,
		Attempts: view,
	}

	s.render(w, r, "admin_results", map[string]any{
		"Courses":  page.Courses,
		"Selected": page.Selected,
		"Attempts": page.Attempts,
	})
}

func (s *Server) handleAdminAttemptDetail(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	if idStr == "" {
		http.Error(w, "id required", 400)
		return
	}
	aid, _ := strconv.ParseInt(idStr, 10, 64)

	meta, answers, err := s.Repo.GetAttemptWithAnswers(r.Context(), aid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// --- аккуратные строки для хедера попытки ---
	started := meta.StartedAt.In(time.Local).Format("02.01.2006 15:04:05")

	finished := "—"
	if meta.FinishedAt != nil {
		finished = meta.FinishedAt.In(time.Local).Format("02.01.2006 15:04:05")
	}

	scoreStr := "—"
	if meta.Score != nil {
		scoreStr = strconv.FormatFloat(*meta.Score, 'f', 0, 64)
	}

	durationStr := "—"
	if meta.DurationSec != nil {
		durationStr = strconv.Itoa(*meta.DurationSec) + " с"
	}

	overtimeStr := "Нет"
	if meta.Overtime {
		overtimeStr = "Да"
	}

	metaView := struct {
		ID        int64
		UserEmail string
		QuizTitle string

		StartedAt  string
		FinishedAt string
		Score      string
		Duration   string
		Overtime   string
	}{
		ID:         meta.ID,
		UserEmail:  meta.UserEmail,
		QuizTitle:  meta.QuizTitle,
		StartedAt:  started,
		FinishedAt: finished,
		Score:      scoreStr,
		Duration:   durationStr,
		Overtime:   overtimeStr,
	}

	// --- детали вопросов ---
	type Row struct {
		Idx        int
		QuestionID int64
		Topic      string
		QType      string
		Text       string
		UserAnswer string
		Correct    string
		Status     string // уже готовая строка для колонки "Статус"
	}

	var out []Row
	for _, a1 := range answers {
		var q struct {
			Text         string   `json:"text"`
			Choices      []string `json:"choices"`
			Correct      []int    `json:"correct"`
			CorrectValue *float64 `json:"correct_value"`
			Accept       []string `json:"accept"`
		}
		_ = json.Unmarshal(a1.Payload, &q)

		var correctText string
		switch a1.QType {
		case "single", "multiple":
			if len(q.Correct) > 0 && len(q.Choices) > 0 {
				parts := []string{}
				for _, idx := range q.Correct {
					if idx >= 0 && idx < len(q.Choices) {
						parts = append(parts, q.Choices[idx])
					}
				}
				correctText = strings.Join(parts, ", ")
			}
		case "numeric":
			if q.CorrectValue != nil {
				correctText = strconv.FormatFloat(*q.CorrectValue, 'f', -1, 64)
			}
		case "text":
			if len(q.Accept) > 0 {
				correctText = strings.Join(q.Accept, " | ")
			}
		}

		var ua string
		var ajson map[string]any
		_ = json.Unmarshal(a1.Answer, &ajson)
		switch a1.QType {
		case "single":
			if i, ok := ajson["chosen"].(float64); ok {
				idx := int(i)
				if idx >= 0 && idx < len(q.Choices) {
					ua = q.Choices[idx]
				} else {
					ua = strconv.Itoa(idx)
				}
			}
		case "multiple":
			if arr, ok := ajson["chosen"].([]any); ok {
				var parts []string
				for _, v := range arr {
					if f, ok := v.(float64); ok {
						idx := int(f)
						if idx >= 0 && idx < len(q.Choices) {
							parts = append(parts, q.Choices[idx])
						}
					}
				}
				ua = strings.Join(parts, ", ")
			}
		case "numeric":
			if v, ok := ajson["value"].(float64); ok {
				ua = strconv.FormatFloat(v, 'f', -1, 64)
			}
		case "text":
			if v, ok := ajson["value"].(string); ok {
				ua = v
			}
		}

		// статус
		status := "—"
		if a1.IsCorrect != nil {
			if *a1.IsCorrect {
				status = "✔"
			} else {
				status = "✘"
			}
		}

		out = append(out, Row{
			Idx:        len(out) + 1,
			QuestionID: a1.QuestionID,
			Topic:      a1.Topic,
			QType:      a1.QType,
			Text:       q.Text,
			UserAnswer: ua,
			Correct:    correctText,
			Status:     status,
		})
	}

	s.render(w, r, "admin_attempt", map[string]any{
		"Meta": metaView,
		"Rows": out,
	})
}

/* ===== Экспорт CSV ===== */

func (s *Server) handleAdminResultsExport(w http.ResponseWriter, r *http.Request) {
	var courseID *int64
	var quizID *int64
	if v := r.URL.Query().Get("course_id"); v != "" {
		if x, err := strconv.ParseInt(v, 10, 64); err == nil {
			courseID = &x
		}
	}
	if v := r.URL.Query().Get("quiz_id"); v != "" {
		if x, err := strconv.ParseInt(v, 10, 64); err == nil {
			quizID = &x
		}
	}
	rows, err := s.Repo.ExportAttempts(r.Context(), courseID, quizID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=\"results.csv\"")
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"attempt_id", "user_email", "course_id", "quiz_id", "quiz_title", "started_at", "finished_at", "score", "duration_sec", "overtime"})
	for _, r0 := range rows {
		finished := ""
		if r0.FinishedAt != nil {
			finished = r0.FinishedAt.Format(time.RFC3339)
		}
		score := ""
		if r0.Score != nil {
			score = strconv.FormatFloat(*r0.Score, 'f', -1, 64)
		}
		dur := ""
		if r0.Duration != nil {
			dur = strconv.Itoa(*r0.Duration)
		}
		_ = cw.Write([]string{
			strconv.FormatInt(r0.AttemptID, 10),
			r0.UserEmail,
			strconv.FormatInt(r0.CourseID, 10),
			strconv.FormatInt(r0.QuizID, 10),
			r0.QuizTitle,
			r0.StartedAt.Format(time.RFC3339),
			finished,
			score,
			dur,
			strconv.FormatBool(r0.Overtime),
		})
	}
	cw.Flush()
}

/*** helpers ***/
func firstOrEmpty(a []string) string {
	if len(a) > 0 {
		return a[0]
	}
	return ""
}

func intSliceToSet(a []int) map[int]struct{} {
	m := map[int]struct{}{}
	for _, v := range a {
		m[v] = struct{}{}
	}
	return m
}

func setEq(a, b map[int]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

func containsCI(hay []string, needle string) bool {
	n := strings.ToLower(strings.TrimSpace(needle))
	for _, v := range hay {
		if strings.ToLower(strings.TrimSpace(v)) == n {
			return true
		}
	}
	return false
}

/* ===== Админ: вопросы ===== */

func (s *Server) handleAdminQuestionsList(w http.ResponseWriter, r *http.Request) {
	cid := int64(1)
	if v := r.URL.Query().Get("course_id"); v != "" {
		if x, err := strconv.ParseInt(v, 10, 64); err == nil {
			cid = x
		}
	}
	topic := strings.TrimSpace(r.URL.Query().Get("topic"))
	qtype := strings.TrimSpace(r.URL.Query().Get("qtype"))
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if x, err := strconv.Atoi(v); err == nil {
			limit = x
		}
	}

	cs, _ := s.Repo.ListCourses(r.Context())
	rows, _ := s.Repo.ListQuestions(r.Context(), cid, topic, qtype, limit)

	s.render(w, r, "admin_questions", map[string]any{
		"Courses":  cs,
		"Selected": cid,
		"Topic":    topic,
		"QType":    qtype,
		"Limit":    limit,
		"Rows":     rows,
	})
}

func (s *Server) handleAdminQuestionEdit(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
		if id == 0 {
			http.Error(w, "id required", 400)
			return
		}
		q, err := s.Repo.GetQuestion(r.Context(), id)
		if err != nil {
			http.Error(w, err.Error(), 404)
			return
		}
		s.render(w, r, "admin_question_edit", map[string]any{"Q": q})

	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
		topic := strings.TrimSpace(r.FormValue("topic"))
		qtype := strings.TrimSpace(r.FormValue("qtype"))
		diff, _ := strconv.Atoi(r.FormValue("difficulty"))
		payload := strings.TrimSpace(r.FormValue("payload"))

		var raw []byte
		if payload != "" {
			if !json.Valid([]byte(payload)) {
				http.Error(w, "payload is not valid JSON", 400)
				return
			}
			raw = []byte(payload)
		}
		if err := s.Repo.UpdateQuestion(r.Context(), id, topic, qtype, diff, raw); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		http.Redirect(w, r, "/admin/questions/edit?id="+strconv.FormatInt(id, 10), http.StatusSeeOther)
	}
}

func (s *Server) handleAdminLogsByUser(w http.ResponseWriter, r *http.Request) {
	uidStr := r.URL.Query().Get("user_id")
	if uidStr == "" {
		// Режим выбора пользователя
		users, _ := s.Repo.ListUsers(r.Context())
		s.render(w, r, "admin_logs", map[string]any{"Users": users})
		return
	}

	uid, _ := strconv.ParseInt(uidStr, 10, 64)

	summary, rows, err := s.Repo.UserLogs(r.Context(), uid)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// --- аккуратно форматируем сводку ---
	lastAtStr := "—"
	if summary.LastAt != nil {
		lastAtStr = summary.LastAt.In(time.Local).Format("02.01.2006 15:04:05")
	}

	type summaryView struct {
		UserEmail string
		Attempts  int
		Correct   int
		Wrong     int
		LastAt    string
	}

	sumView := summaryView{
		UserEmail: summary.UserEmail,
		Attempts:  summary.Attempts,
		Correct:   summary.Correct,
		Wrong:     summary.Wrong,
		LastAt:    lastAtStr,
	}

	// --- приводим строки логов к виду для шаблона ---
	type rowView struct {
		When   string
		Action string
		Detail string
	}

	viewRows := make([]rowView, 0, len(rows))
	for _, r0 := range rows {
		whenStr := r0.When.In(time.Local).Format("02.01.2006 15:04:05")

		status := "—"
		if r0.IsCorrect != nil {
			if *r0.IsCorrect {
				status = "верно"
			} else {
				status = "неверно"
			}
		}

		action := "Ответ по вопросу"
		detail := "Тема: " + r0.Topic +
			", тип: " + r0.QType +
			", статус: " + status +
			", попытка #" + strconv.FormatInt(r0.AttemptID, 10)

		viewRows = append(viewRows, rowView{
			When:   whenStr,
			Action: action,
			Detail: detail,
		})
	}

	s.render(w, r, "admin_logs", map[string]any{
		"Summary": sumView,
		"Rows":    viewRows,
		"UserID":  uid,
	})
}
