package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "github.com/mattn/go-sqlite3"
)

const dateFormat = "20060102"

var db *sql.DB

type Task struct {
	ID      string `json:"id"`
	Date    string `json:"date"`
	Title   string `json:"title"`
	Comment string `json:"comment"`
	Repeat  string `json:"repeat"`
}

func nextDate(now time.Time, date string, repeat string) (string, error) {
	// Парсим исходную дату
	taskDate, err := time.Parse(dateFormat, date)
	if err != nil {
		return "", fmt.Errorf("неверный формат даты: %v", err)
	}

	// Разбираем правило повторения
	if repeat == "" {
		return "", errors.New("пустое правило повторения")
	}

	// Если правило - каждый год (y)
	if repeat == "y" {
		// Добавляем 1 год
		nextDate := taskDate.AddDate(1, 0, 0)
		// Если следующая дата меньше текущей, повторяем на следующий год
		for nextDate.Before(now) {
			nextDate = nextDate.AddDate(1, 0, 0)
		}
		return nextDate.Format("20060102"), nil
	}

	// Если правило - дни (d <число>)
	if len(repeat) > 1 && repeat[:1] == "d" {
		var days int
		_, err := fmt.Sscanf(repeat, "d %d", &days)
		if err != nil {
			return "", errors.New("неправильный формат правила d")
		}
		if days < 1 || days > 400 {
			return "", errors.New("недопустимый интервал дней")
		}

		// Добавляем указанное количество дней
		nextDate := taskDate.AddDate(0, 0, days)
		// Если следующая дата меньше текущей, добавляем дни дальше
		for nextDate.Before(now) {
			nextDate = nextDate.AddDate(0, 0, days)
		}
		return nextDate.Format("20060102"), nil
	}

	// Если правило не поддерживается
	return "", errors.New("неподдерживаемый формат правила")
}

// POST-обработчик для добавления задачи
func addTaskHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}

	// Декодируем JSON
	var task Task
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&task)
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка десериализации JSON: %v", err), http.StatusBadRequest)
		return
	}

	// Проверяем обязательные поля
	if task.Title == "" {
		http.Error(w, `{"error":"Не указан заголовок задачи"}`, http.StatusBadRequest)
		return
	}

	// Проверяем, если поле Repeat пустое, то ошибка не генерируется
	if task.Repeat == "" {
		// Это условие может быть пропущено, если правило повторения необязательно
	}

	// Если дата не указана, берем сегодняшнюю
	if task.Date == "" {
		task.Date = time.Now().Format(dateFormat)
	}

	// Проверка правильности формата даты
	now, err := time.Parse(dateFormat, time.Now().Format(dateFormat))
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка при парсинге текущей даты: %v", err), http.StatusInternalServerError)
		return
	}

	taskDate, err := time.Parse("20060102", task.Date)
	if err != nil {
		http.Error(w, `{"error":"Неверный формат даты"}`, http.StatusBadRequest)
		return
	}

	// Если дата меньше текущей и задано правило повторения
	if taskDate.Before(now) {
		if task.Repeat == "" {
			task.Date = time.Now().Format("20060102") // Задача с датой меньше текущей будет перемещена на сегодня
		} else {
			// Если задано правило повторения, используем NextDate для вычисления следующей даты
			nextDate, err := nextDate(now, task.Date, task.Repeat)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
				return
			}
			task.Date = nextDate // Пересчитываем дату с учетом правила повторения
		}
	}

	// Открываем базу данных
	db, err := sql.Open("sqlite3", "./scheduler.db")
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка базы данных: %v", err), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// Добавляем задачу в базу данных
	query := `INSERT INTO scheduler (date, title, comment, repeat) VALUES (?, ?, ?, ?)`
	res, err := db.Exec(query, task.Date, task.Title, task.Comment, task.Repeat)
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка при добавлении задачи в базу данных: %v", err), http.StatusInternalServerError)
		return
	}

	// Получаем ID новой записи
	id, err := res.LastInsertId()
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка при получении ID задачи: %v", err), http.StatusInternalServerError)
		return
	}

	// Формируем ответ
	response := map[string]interface{}{
		"id": id,
	}

	// Отправляем ответ в формате JSON
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	json.NewEncoder(w).Encode(response)
}

func nextDateHandler(w http.ResponseWriter, r *http.Request) {
	// Получаем параметры из запроса
	nowStr := r.FormValue("now")
	dateStr := r.FormValue("date")
	repeat := r.FormValue("repeat")

	// Преобразуем параметры в тип time.Time
	now, err := time.Parse("20060102", nowStr)
	if err != nil {
		http.Error(w, fmt.Sprintf("неверный формат 'now' (%s)", err), http.StatusBadRequest)
		return
	}

	// Вызываем функцию NextDate
	nextDate, err := nextDate(now, dateStr, repeat)
	if err != nil {
		http.Error(w, fmt.Sprintf("ошибка: %v", err), http.StatusBadRequest)
		return
	}

	// Отправляем результат
	w.Write([]byte(nextDate))
}

func main() {
	// Создаем маршрутизатор chi
	r := chi.NewRouter()

	// Открываем соединение с базой данных
	var err error
	db, err = sql.Open("sqlite3", "./scheduler.db")
	if err != nil {
		fmt.Println("Ошибка при подключении к базе данных:", err)
		return
	}
	defer db.Close() // Закрытие базы данных при завершении работы приложения

	// Добавляем middleware
	r.Use(middleware.Logger)    // Логируем запросы
	r.Use(middleware.Recoverer) // Восстанавливаем приложение после паник

	// Проверка и создание базы данных
	appPath, err := os.Executable()
	if err != nil {
		fmt.Println(err)
	}
	dbFile := filepath.Join(filepath.Dir(appPath), "scheduler.db")
	installDatabase(dbFile) // Создаем базу данных, если она не существует

	// Обработка API
	r.Route("/api", func(r chi.Router) {
		r.Post("/task", addTaskHandler)
		r.Get("/tasks", getTasksHandler)
		r.Get("/task", getTaskByIDHandler)
		r.Put("/task", editTaskHandler)
		r.Get("/nextdate", nextDateHandler)
		r.Post("/task/done", taskDoneHandler)
		r.Delete("/task", deleteTaskHandler)
	})

	// Обработка статических файлов
	webDir := "./web"
	r.Handle("/*", http.StripPrefix("/", http.FileServer(http.Dir(webDir))))

	// Запуск сервера
	fmt.Println("Сервер запущен на порту 7540...")
	if err := http.ListenAndServe(":7540", r); err != nil {
		fmt.Println("Ошибка при запуске сервера:", err)
	}
}

// Функция для проверки наличия базы данных и её создания
func installDatabase(dbFile string) {
	// Проверяем, существует ли база данных
	_, err := os.Stat(dbFile)
	var install bool
	if err != nil {
		install = true // Если базы нет, создаем новую
	}

	// Открываем или создаем базу данных
	db, err := sql.Open("sqlite3", dbFile)
	if err != nil {
		fmt.Println(err)
	}
	defer db.Close()

	// Если база данных новая, создаем таблицу и индекс
	if install {
		createTableAndIndex(db)
	}
}

// Функция для создания таблицы и индекса
func createTableAndIndex(db *sql.DB) {
	// Создаем таблицу scheduler
	createTableSQL := `
    CREATE TABLE IF NOT EXISTS scheduler (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        date TEXT NOT NULL,
        title TEXT NOT NULL,
        comment TEXT,
        repeat TEXT
    );
    `
	_, err := db.Exec(createTableSQL)
	if err != nil {
		fmt.Println(err)
	}

	// Создаем индекс по полю date
	createIndexSQL := `CREATE INDEX IF NOT EXISTS idx_date ON scheduler (date);`
	_, err = db.Exec(createIndexSQL)
	if err != nil {
		fmt.Println(err)
	}
}

// GET-обработчик для получения списка задач
func getTasksHandler(w http.ResponseWriter, r *http.Request) {
	// Получаем параметр поиска
	search := r.FormValue("search")
	limit := 50 // Максимальное количество задач, которые мы вернем

	// Открываем базу данных
	db, err := sql.Open("sqlite3", "./scheduler.db")
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Ошибка при подключении к базе данных: %v"}`, err), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// Формируем SQL-запрос с учетом поиска
	var query string
	var args []interface{}
	if search != "" {
		// Если есть параметр поиска, ищем по названию или комментарию
		query = `SELECT id, date, title, comment, repeat FROM scheduler WHERE title LIKE ? OR comment LIKE ? ORDER BY date LIMIT ?`
		args = []interface{}{"%" + search + "%", "%" + search + "%", limit}
	} else {
		// Если параметра поиска нет, просто выбираем задачи, отсортированные по дате
		query = `SELECT id, date, title, comment, repeat FROM scheduler ORDER BY date LIMIT ?`
		args = []interface{}{limit}
	}

	// Выполняем запрос
	rows, err := db.Query(query, args...)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Ошибка при выполнении запроса: %v"}`, err), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	// Создаем слайс для хранения задач
	var tasks []map[string]string

	// Обрабатываем результаты запроса
	for rows.Next() {
		var id, date, title, comment, repeat string
		err := rows.Scan(&id, &date, &title, &comment, &repeat)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Ошибка при обработке строки результата: %v"}`, err), http.StatusInternalServerError)
			return
		}

		// Добавляем задачу в слайс
		tasks = append(tasks, map[string]string{
			"id":      id,
			"date":    date,
			"title":   title,
			"comment": comment,
			"repeat":  repeat,
		})
	}

	if err := rows.Err(); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Ошибка при обработке строк из базы данных: %v"}`, err), http.StatusInternalServerError)
		return
	}

	// Если задач нет, возвращаем пустой список
	if tasks == nil {
		tasks = []map[string]string{}
	}

	// Формируем ответ в формате JSON
	response := map[string]interface{}{
		"tasks": tasks,
	}

	// Отправляем ответ
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Ошибка при кодировании ответа: %v"}`, err), http.StatusInternalServerError)
	}
}

// GET-обработчик для получения данных задачи по ID
func getTaskByIDHandler(w http.ResponseWriter, r *http.Request) {
	// Получаем параметр id из запроса
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, `{"error": "Не указан идентификатор"}`, http.StatusBadRequest)
		return
	}

	// Открываем базу данных
	db, err := sql.Open("sqlite3", "./scheduler.db")
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "Ошибка при подключении к базе данных: %v"}`, err), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// Запрос к базе данных для получения задачи
	var task Task
	query := "SELECT id, date, title, comment, repeat FROM scheduler WHERE id = ?"
	err = db.QueryRow(query, id).Scan(&task.ID, &task.Date, &task.Title, &task.Comment, &task.Repeat)
	if err != nil {
		// Если задача не найдена
		if err == sql.ErrNoRows {
			http.Error(w, `{"error": "Задача не найдена"}`, http.StatusNotFound)
		} else {
			http.Error(w, fmt.Sprintf(`{"error": "Ошибка при запросе к базе данных: %v"}`, err), http.StatusInternalServerError)
		}
		return
	}

	// Формируем ответ
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	if err := json.NewEncoder(w).Encode(task); err != nil {
		http.Error(w, fmt.Sprintf(`{"error": "Ошибка при кодировании ответа: %v"}`, err), http.StatusInternalServerError)
	}
}

// PUT-обработчик для редактирования задачи
func editTaskHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "Метод не поддерживается", http.StatusMethodNotAllowed)
		return
	}

	// Декодируем JSON
	var task Task
	decoder := json.NewDecoder(r.Body)
	err := decoder.Decode(&task)
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка десериализации JSON: %v", err), http.StatusBadRequest)
		return
	}

	// Проверяем обязательные поля
	if task.Title == "" {
		http.Error(w, `{"error": "Не указан заголовок задачи"}`, http.StatusBadRequest)
		return
	}

	// Проверяем формат даты (должен быть YYYYMMDD)
	if task.Date != "" {
		_, err := time.Parse(dateFormat, task.Date)
		if err != nil {
			http.Error(w, `{"error": "Неверный формат даты"}`, http.StatusBadRequest)
			return
		}
	}

	// Проверяем валидность Repeat
	if task.Repeat != "" {
		_, err := nextDate(time.Now(), task.Date, task.Repeat)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error": "Неверное правило повторения: %v"}`, err), http.StatusBadRequest)
			return
		}
	}

	// Если дата не указана, берем сегодняшнюю
	if task.Date == "" {
		task.Date = time.Now().Format("20060102")
	}

	// Открываем базу данных
	db, err := sql.Open("sqlite3", "./scheduler.db")
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка базы данных: %v", err), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// Проверка существования задачи в базе
	var existingID string
	query := "SELECT id FROM scheduler WHERE id = ?"
	err = db.QueryRow(query, task.ID).Scan(&existingID)
	if err == sql.ErrNoRows {
		http.Error(w, `{"error": "Задача не найдена"}`, http.StatusNotFound)
		return
	} else if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка базы данных: %v", err), http.StatusInternalServerError)
		return
	}

	// Обновляем задачу в базе данных
	updateQuery := "UPDATE scheduler SET date = ?, title = ?, comment = ?, repeat = ? WHERE id = ?"
	_, err = db.Exec(updateQuery, task.Date, task.Title, task.Comment, task.Repeat, task.ID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Ошибка при обновлении задачи: %v", err), http.StatusInternalServerError)
		return
	}

	// Отправляем успешный ответ
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.Write([]byte("{}"))
}

func taskDoneHandler(w http.ResponseWriter, r *http.Request) {
	// Получаем ID задачи из параметров запроса
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, `{"error":"Не указан идентификатор задачи"}`, http.StatusBadRequest)
		return
	}

	// Открываем базу данных
	db, err := sql.Open("sqlite3", "./scheduler.db")
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Ошибка при подключении к базе данных: %v"}`, err), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// Получаем информацию о задаче
	var task Task
	query := "SELECT id, date, repeat FROM scheduler WHERE id = ?"
	err = db.QueryRow(query, id).Scan(&task.ID, &task.Date, &task.Repeat)
	if err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, `{"error":"Задача не найдена"}`, http.StatusNotFound)
		} else {
			http.Error(w, fmt.Sprintf(`{"error":"Ошибка при запросе задачи: %v"}`, err), http.StatusInternalServerError)
		}
		return
	}

	// Обрабатываем задачу
	if task.Repeat == "" {
		// Удаляем одноразовую задачу
		deleteQuery := "DELETE FROM scheduler WHERE id = ?"
		_, err := db.Exec(deleteQuery, id)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Ошибка при удалении задачи: %v"}`, err), http.StatusInternalServerError)
			return
		}
	} else {
		// Рассчитываем следующую дату для периодической задачи
		now := time.Now()
		nextDate, err := nextDate(now, task.Date, task.Repeat)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Ошибка при расчете следующей даты: %v"}`, err), http.StatusInternalServerError)
			return
		}

		// Обновляем дату задачи
		updateQuery := "UPDATE scheduler SET date = ? WHERE id = ?"
		_, err = db.Exec(updateQuery, nextDate, id)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"Ошибка при обновлении даты задачи: %v"}`, err), http.StatusInternalServerError)
			return
		}
	}

	// Возвращаем успешный ответ
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.Write([]byte("{}"))
}

func deleteTaskHandler(w http.ResponseWriter, r *http.Request) {
	// Получаем ID задачи из параметров запроса
	id := r.URL.Query().Get("id")
	if id == "" {
		http.Error(w, `{"error":"Не указан идентификатор задачи"}`, http.StatusBadRequest)
		return
	}

	// Открываем базу данных
	db, err := sql.Open("sqlite3", "./scheduler.db")
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Ошибка при подключении к базе данных: %v"}`, err), http.StatusInternalServerError)
		return
	}
	defer db.Close()

	// Удаляем задачу
	deleteQuery := "DELETE FROM scheduler WHERE id = ?"
	res, err := db.Exec(deleteQuery, id)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"Ошибка при удалении задачи: %v"}`, err), http.StatusInternalServerError)
		return
	}

	// Проверяем, была ли задача удалена
	rowsAffected, err := res.RowsAffected()
	if err != nil || rowsAffected == 0 {
		http.Error(w, `{"error":"Задача не найдена"}`, http.StatusNotFound)
		return
	}

	// Возвращаем успешный ответ
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.Write([]byte("{}"))
}
