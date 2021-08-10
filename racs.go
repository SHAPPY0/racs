package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

type state int

const (
	NONE            state = 0
	CREATING        state = 1
	CREATE_SUCCESS  state = 2
	CREATE_ERROR    state = 3
	CLEANING        state = 4
	CLEAN_SUCCESS   state = 5
	CLEAN_ERROR     state = 6
	CLONING         state = 7
	CLONE_SUCCESS   state = 8
	CLONE_ERROR     state = 9
	PREPARING       state = 10
	PREPARE_SUCCESS state = 11
	PREPARE_ERROR   state = 12
	PULLING         state = 13
	PULL_SUCCESS    state = 14
	PULL_ERROR      state = 15
	BUILDING        state = 16
	BUILD_SUCCESS   state = 17
	BUILD_ERROR     state = 18
	PACKAGING       state = 19
	PACKAGE_SUCCESS state = 20
	PACKAGE_ERROR   state = 21
	PUSHING         state = 22
	PUSH_SUCCESS    state = 23
	PUSH_ERROR      state = 24
)

func (s state) String() string {
	return [25]string{"NONE",
		"CREATING", "CREATE_SUCCESS", "CREATE_ERROR",
		"CLEANING", "CLEAN_SUCCESS", "CLEAN_ERROR",
		"CLONING", "CLONE_SUCCESS", "CLONE_ERROR",
		"PREPARING", "PREPARE_SUCCESS", "PREPARE_ERROR",
		"PULLING", "PULL_SUCCESS", "PULL_ERROR",
		"BUILDING", "BUILD_SUCCESS", "BUILD_ERROR",
		"PACKAGING", "PACKAGE_SUCCESS", "PACKAGE_ERROR",
		"PUSHING", "PUSH_SUCCESS", "PUSH_ERROR"}[s]
}

type task struct {
	id    int
	kind  string
	state string
	time  string
}

type action struct {
	state   state
	command string
	args    []string
}

type project struct {
	id          int
	name        string
	destination string
	tag         string
	state       state
	version     int
	tasks       []*task
	queue       chan action
}

var db *sql.DB
var projects = map[int]*project{}
var projectAbs, _ = filepath.Abs("projects")

func (p *project) taskCreate(state state, command string, args ...string) {
	log.Printf("taskCreate(%d, %s, %s, %v)", p.id, state, command, args)
	p.queue <- action{state, command, args}
}

func projectRoutine(p *project) {
	for {
		log.Printf("Project %d waiting for tasks", p.id)
		a := <-p.queue
		log.Printf("Project %d received task %v", p.id, a)
		p.state = a.state
		if len(a.command) > 0 {
			var id int
			var time string
			err := db.QueryRow(`INSERT INTO tasks(project, type, state, time)
				VALUES(?, ?, 'RUNNING', datetime('now')) RETURNING id, time`, p.id, p.state.String()).Scan(&id, &time)
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("Creating task %d:%d", p.id, id)
			t := &task{id, p.state.String(), "RUNNING", time}
			p.tasks = append(p.tasks, t)
			if len(p.tasks) > 5 {
				p.tasks = p.tasks[1:]
			}
			taskRoot := fmt.Sprintf("tasks/%d", id)
			os.Mkdir(taskRoot, 0777)
			log.Printf("task %s %v", a.command, a.args)
			cmd := exec.Command(a.command, a.args...)
			out, _ := os.Create(fmt.Sprintf("%s/out.log", taskRoot))
			cmd.Stdout = out
			cmd.Stderr = out
			err = cmd.Run()
			if err != nil {
				t.state = "ERROR"
				p.state += 2
			} else {
				t.state = "SUCCESS"
				p.state += 1
			}
			out.Close()
			log.Printf("Task %d completed", id)
			db.Exec(`UPDATE projects SET state = ? WHERE id = ?`, p.state.String(), p.id)
			db.Exec(`UPDATE tasks SET state = ? WHERE id = ?`, t.state, t.id)
		}
		switch p.state {
		case CREATE_SUCCESS:
			p.taskCreate(CLEANING, "/usr/bin/rm", "-rfv", fmt.Sprintf("%s/%d/workspace/source", projectAbs, p.id))
		case CLEAN_SUCCESS:
			rows, _ := db.Query(`SELECT source, branch FROM projects WHERE id = ?`, p.id)
			rows.Next()
			var url, branch string
			rows.Scan(&url, &branch)
			p.taskCreate(CLONING, "/usr/bin/git", "clone", "-v", "--recursive", "-b", branch, url, fmt.Sprintf("%s/%d/workspace/source", projectAbs, p.id))
		case CLONE_SUCCESS:
			p.taskCreate(PREPARING, "/usr/bin/podman", "build", "--squash", "-f", fmt.Sprintf("%s/%d/BuildSpec", projectAbs, p.id), "-t", fmt.Sprintf("builder-%d", p.id), fmt.Sprintf("%s/%d/context", projectAbs, p.id))
		case PREPARE_SUCCESS:
			p.taskCreate(PULLING, "/usr/bin/git", "-C", fmt.Sprintf("%s/%d/workspace/source", projectAbs, p.id), "pull", "--recurse-submodules")
		case PULL_SUCCESS:
			p.taskCreate(BUILDING, "/usr/bin/podman", "run", "--network", "host", "-v", fmt.Sprintf("%s/%d/workspace:/workspace", projectAbs, p.id), "--read-only", fmt.Sprintf("builder-%d", p.id))
		case BUILD_SUCCESS:
			p.version += 1
			db.Exec(`UPDATE projects SET version = ? WHERE id = ?`, p.version, p.id)
			tag := strings.Replace(p.tag, "$VERSION", strconv.Itoa(p.version), -1)
			p.taskCreate(PACKAGING, "/usr/bin/podman", "build", "-v", fmt.Sprintf("%s/%d/workspace:/workspace", projectAbs, p.id), "--squash", "-f", fmt.Sprintf("%s/%d/PackageSpec", projectAbs, p.id), "-t", tag, fmt.Sprintf("%s/%d/context", projectAbs, p.id))
		case PACKAGE_SUCCESS:
			tag := strings.Replace(p.tag, "$VERSION", strconv.Itoa(p.version), -1)
			p.taskCreate(PUSHING, "/usr/bin/podman", "push", tag, fmt.Sprintf("%s/%s", p.destination, tag))
		}
		log.Printf("Project %d finished task %v", p.id, a)
	}
}

func projectCreate(name string, url string, branch string, destination string, tag string) *project {
	var id int
	db.QueryRow(`	INSERT INTO projects(name, source, branch, destination, tag, state, version)
		VALUES(?, ?, ?, ?, ?, 'CLONING', 0) RETURNING id`, name, url, branch, destination, tag).Scan(&id)
	log.Printf("Project created %s %s %s %s\n", id, name, url, branch)
	os.Mkdir(fmt.Sprintf("%s/%d", projectAbs, id), 0777)
	os.Mkdir(fmt.Sprintf("%s/%d/context", projectAbs, id), 0777)
	os.Mkdir(fmt.Sprintf("%s/%d/workspace", projectAbs, id), 0777)
	p := &project{id, name, destination, tag, CREATE_SUCCESS, 0, make([]*task, 0), make(chan action, 10)}
	projects[p.id] = p
	go projectRoutine(p)
	//p.taskCreate(CLONING, "/usr/bin/git", "clone", "-v", "--recursive", "-b", branch, url, fmt.Sprintf("%s/%d/workspace/source", projectAbs, id))
	return p
}

var staticPath, _ = filepath.Abs("static")

func loadStatic(path string) ([]byte, error) {
	path = filepath.Clean(path)
	if path == "." {
		return nil, errors.New("Not found")
	}
	log.Printf("Serving %s%s", staticPath, path)
	return ioutil.ReadFile(staticPath + path)
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/" {
		path = "/index.xhtml"
	}
	var contentType string
	switch filepath.Ext(path) {
	case ".xhtml":
		contentType = "application/xhtml+xml"
	case ".js":
		contentType = "text/javascript"
	case ".css":
		contentType = "text/css"
	case ".ico":
		contentType = "image/png"
	default:
		contentType = ""
	}
	content, err := loadStatic(path)
	if err != nil {
		w.WriteHeader(404)
		w.Write([]byte("Not found"))
	} else {
		w.Header().Add("Content-Type", contentType)
		w.Write(content)
	}
}

func handleProjectList(w http.ResponseWriter, r *http.Request) {
	result := make([]map[string]interface{}, 0)
	for id, p := range projects {
		tasks := make([]interface{}, 0)
		for _, task := range p.tasks {
			tasks = append(tasks, map[string]interface{}{
				"id":    task.id,
				"type":  task.kind,
				"state": task.state,
				"time":  task.time,
			})
		}
		result = append(result, map[string]interface{}{
			"id":      id,
			"name":    p.name,
			"state":   p.state.String(),
			"tasks":   tasks,
			"version": p.version,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i]["id"].(int) < result[j]["id"].(int)
	})
	w.Header().Add("Content-Type", "application/json")
	j, _ := json.Marshal(result)
	w.Write(j)
}

func getParams(r *http.Request) map[string]string {
	contentType := r.Header.Get("Content-Type")
	params := make(map[string]string)
	if strings.HasPrefix(contentType, "application/json") {
		body, _ := ioutil.ReadAll(r.Body)
		var j map[string]interface{}
		json.Unmarshal(body, &j)
		for name, value := range j {
			params[name] = fmt.Sprint(value)
		}
	} else if strings.HasPrefix(contentType, "multipart/form-data") {
		r.ParseMultipartForm(10000000)
		for name, values := range r.MultipartForm.Value {
			params[name] = values[0]
		}
	} else {
		r.ParseForm()
		for name, values := range r.Form {
			params[name] = values[0]
		}
	}
	return params
}

func handleProjectStatus(w http.ResponseWriter, r *http.Request) {

}

func handleProjectCreate(w http.ResponseWriter, r *http.Request) {
	params := getParams(r)
	name := params["name"]
	url := params["url"]
	branch := params["branch"]
	destination := params["destination"]
	tag := params["tag"]
	p := projectCreate(name, url, branch, destination, tag)
	redirect := params["redirect"]
	if len(redirect) > 0 {
		w.Header().Add("Location", redirect)
		w.WriteHeader(303)
	} else {
		w.WriteHeader(201)
		w.Write([]byte(strconv.Itoa(p.id)))
	}
}

func handleProjectUpload(w http.ResponseWriter, r *http.Request) {
	params := getParams(r)
	id, _ := strconv.Atoi(params["id"])
	name := params["name"]
	file := r.MultipartForm.File["file"][0]
	p := projects[id]
	name = filepath.Clean(name)
	if p == nil {
		w.WriteHeader(500)
	} else if name == "." {
		w.WriteHeader(500)
	} else {
		rd, _ := file.Open()
		wr, _ := os.Create(fmt.Sprintf("%s/%d/%s", projectAbs, id, name))
		io.Copy(wr, rd)
		wr.Close()
		rd.Close()
		redirect := params["redirect"]
		if len(redirect) > 0 {
			w.Header().Add("Location", redirect)
			w.WriteHeader(303)
		} else {
			w.WriteHeader(200)
			w.Write([]byte("OK"))
		}
	}
}

func handleProjectBuild(w http.ResponseWriter, r *http.Request) {
	params := getParams(r)
	id, _ := strconv.Atoi(params["id"])
	stage := params["stage"]
	p := projects[id]
	switch stage {
	case "clean":
		p.taskCreate(CREATE_SUCCESS, "")
	case "prepare":
		p.taskCreate(CLONE_SUCCESS, "")
	case "pull":
		p.taskCreate(PREPARE_SUCCESS, "")
	case "build":
		p.taskCreate(PULL_SUCCESS, "")
	case "package":
		p.taskCreate(BUILD_SUCCESS, "")
	}
	w.WriteHeader(200)
	w.Write([]byte("OK"))
}

func handleTaskLogs(w http.ResponseWriter, r *http.Request) {
	params := getParams(r)
	id, _ := strconv.Atoi(params["id"])
	offset, _ := strconv.ParseInt(params["offset"], 10, 64)
	file, _ := os.Open(fmt.Sprintf("tasks/%d/out.log", id))
	file.Seek(offset, 0)
	bytes, _ := ioutil.ReadAll(file)
	w.Header().Add("Content-Type", "text/plain")
	w.Write(bytes)
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	var err error

	os.Mkdir("projects", 0777)
	os.Mkdir("tasks", 0777)

	db, err = sql.Open("sqlite3", "file:main.db?cache=shared")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	//db.SetMaxOpenConns(1)

	stats := []string{
		`CREATE TABLE IF NOT EXISTS users(
			name STRING PRIMARY KEY,
			passwd STRING,
			salt STRING,
			role STRING
		)`,
		`CREATE TABLE IF NOT EXISTS projects(
			id INTEGER PRIMARY KEY,
			name STRING,
			source STRING,
			branch STRING,
			destination STRING,
			tag STRING,
			state STRING,
			version INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS tasks(
			id INTEGER PRIMARY KEY,
			project INTEGER,
			type STRING,
			state STRING,
			time STRING
		)`,
		`CREATE TABLE IF NOT EXISTS members(
			project INTEGER,
			user STRING,
			role STRING
		)`}

	for _, stat := range stats {
		_, err := db.Exec(stat)
		if err != nil {
			log.Printf("%q: %s\n", err, stat)
			return
		}
	}

	states := make(map[string]state)
	for state := NONE; state <= PUSH_ERROR; state += 1 {
		states[state.String()] = state
	}

	rows, err := db.Query(`SELECT id, name, destination, tag, state, version FROM projects`)
	for rows.Next() {
		var id int
		var name string
		var destination string
		var tag string
		var stateName string
		var version int
		rows.Scan(&id, &name, &destination, &tag, &stateName, &version)
		state := states[stateName]
		p := &project{id, name, destination, tag, state, version, make([]*task, 0), make(chan action, 10)}
		projects[p.id] = p
		go projectRoutine(p)
	}
	rows, err = db.Query(`SELECT project, id, type, state, time FROM tasks ORDER BY id`)
	for rows.Next() {
		var pid int
		var id int
		var kind string
		var state string
		var time string
		rows.Scan(&pid, &id, &kind, &state, &time)
		p := projects[pid]
		if p != nil {
			p.tasks = append(p.tasks, &task{id, kind, state, time})
			if len(p.tasks) > 5 {
				p.tasks = p.tasks[1:]
			}
		}
	}

	http.HandleFunc("/", handleRoot)
	http.HandleFunc("/project/list", handleProjectList)
	http.HandleFunc("/project/status", handleProjectStatus)
	http.HandleFunc("/project/create", handleProjectCreate)
	http.HandleFunc("/project/upload", handleProjectUpload)
	http.HandleFunc("/project/build", handleProjectBuild)
	http.HandleFunc("/task/logs", handleTaskLogs)
	http.ListenAndServe(":8081", nil)
}
