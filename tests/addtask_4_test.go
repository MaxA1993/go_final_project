package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func requestJSON(apipath string, values map[string]any, method string) ([]byte, error) {
	var (
		data []byte
		err  error
	)

	if len(values) > 0 {
		data, err = json.Marshal(values)
		if err != nil {
			return nil, err
		}
	}
	var resp *http.Response

	req, err := http.NewRequest(method, getURL(apipath), bytes.NewBuffer(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	if len(Token) > 0 {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, err
		}
		jar.SetCookies(req.URL, []*http.Cookie{
			{
				Name:  "token",
				Value: Token,
			},
		})
		client.Jar = jar
	}

	resp, err = client.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.Body != nil {
		defer resp.Body.Close()
	}
	return io.ReadAll(resp.Body)
}

func postJSON(apipath string, values map[string]any, method string) (map[string]any, error) {
	var (
		m   map[string]any
		err error
	)

	body, err := requestJSON(apipath, values, method)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(body, &m)
	return m, err
}

type task struct {
	date    string
	title   string
	comment string
	repeat  string
}

func TestAddTask(t *testing.T) {
	db := openDB(t)
	defer db.Close()

	// Тестовые задачи с потенциальными ошибками
	tbl := []task{
		{"20240129", "", "", ""},            // Пустой заголовок
		{"20240192", "Qwerty", "", ""},      // Некорректный формат даты
		{"28.01.2024", "Заголовок", "", ""}, // Некорректный формат даты

	}

	// Проходим по всем задачам и проверяем, что ошибка должна быть
	for _, v := range tbl {
		m, err := postJSON("api/task", map[string]any{
			"date":    v.date,
			"title":   v.title,
			"comment": v.comment,
			"repeat":  v.repeat,
		}, http.MethodPost)
		assert.NoError(t, err)

		// Проверяем наличие ошибки
		e, ok := m["error"]
		if v.repeat == "" {
			// Для пустого поля repeat ожидаем ошибку
			assert.True(t, ok && len(fmt.Sprint(e)) > 0, "Ожидается ошибка для задачи %v", v)
		} else if v.repeat == "ooops" {
			// Для некорректного значения repeat тоже ожидаем ошибку
			assert.True(t, ok && len(fmt.Sprint(e)) > 0, "Ожидается ошибка для задачи %v", v)
		} else {
			// Задачи с корректным repeat не должны вызвать ошибку
			assert.False(t, ok || len(fmt.Sprint(e)) > 0, "Неожиданная ошибка для задачи %v", v)
		}
	}

	// Функция для проверки валидных задач
	now := time.Now()

	check := func() {
		for _, v := range tbl {
			// Обработка случая с датой "today"
			today := v.date == "today"
			if today {
				v.date = now.Format(`20060102`)
			}
			m, err := postJSON("api/task", map[string]any{
				"date":    v.date,
				"title":   v.title,
				"comment": v.comment,
				"repeat":  v.repeat,
			}, http.MethodPost)
			assert.NoError(t, err)

			e, ok := m["error"]
			// Проверяем, что ошибки не возникло, если задача валидна
			if ok && len(fmt.Sprint(e)) > 0 {
				t.Errorf("Неожиданная ошибка %v для задачи %v", e, v)
				continue
			}

			var task Task
			var mid any
			mid, ok = m["id"]
			if !ok {
				t.Errorf("Не возвращён id для задачи %v", v)
				continue
			}

			id := fmt.Sprint(mid)

			// Проверка данных задачи в базе данных
			err = db.Get(&task, `SELECT * FROM scheduler WHERE id=?`, id)
			assert.NoError(t, err)
			assert.Equal(t, id, strconv.FormatInt(task.ID, 10))

			// Проверка на правильность данных
			assert.Equal(t, v.title, task.Title)
			assert.Equal(t, v.comment, task.Comment)
			assert.Equal(t, v.repeat, task.Repeat)

			// Проверка даты
			if task.Date < now.Format(`20060102`) {
				t.Errorf("Дата не может быть меньше сегодняшней %v", v)
				continue
			}
			if today && task.Date != now.Format(`20060102`) {
				t.Errorf("Дата должна быть сегодняшняя %v", v)
			}
		}
	}

	// Дополнительные тесты с новыми задачами
	tbl = []task{
		{"", "Заголовок", "", ""}, // Пустая дата
		{"20231220", "Сделать что-нибудь", "Хорошо отдохнуть", ""}, // Пустое правило повторения
		{"20240108", "Уроки", "", "d 10"},                          // Неверное правило повторения
		{"20240102", "Отдых в Сочи", "На лыжах", "y"},              // Правильное правило повторения
		{"today", "Фитнес", "", "d 1"},                             // Задача с датой "today"
		{"today", "Шмитнес", "", ""},                               // Задача без повтора
	}
	check()

	if FullNextDate {
		tbl = []task{
			{"20240129", "Сходить в магазин", "", "d 1,3,5"},
		}
		check()
	}
}
