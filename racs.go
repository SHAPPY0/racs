package main

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/msteinert/pam"
	"github.com/withmandala/go-log"
)

var logger = log.New(os.Stderr)

type state int

const (
	DELETING             state = -3
	DELETE_ERROR         state = -2
	DELETE_SUCCESS       state = -1
	NONE                 state = 0
	CREATING             state = 1
	CREATE_ERROR         state = 2
	CREATE_SUCCESS       state = 3
	CLEANING             state = 4
	CLEAN_ERROR          state = 5
	CLEAN_SUCCESS        state = 6
	CLONING              state = 7
	CLONE_ERROR          state = 8
	CLONE_SUCCESS        state = 9
	PREPARING            state = 10
	PREPARE_ERROR        state = 11
	PREPARE_SUCCESS      state = 12
	PULLING              state = 13
	PULL_ERROR           state = 14
	PULL_SUCCESS         state = 15
	BUILDING             state = 16
	BUILD_ERROR          state = 17
	BUILD_SUCCESS        state = 18
	PREPACKAGING         state = 19
	PREPACKAGING_ERROR   state = 20
	PREPACKAGING_SUCCESS state = 21
	PACKAGING            state = 22
	PACKAGE_ERROR        state = 23
	PACKAGE_SUCCESS      state = 24
	PUSHING              state = 25
	PUSH_ERROR           state = 26
	PUSH_SUCCESS         state = 27
	TAGGING              state = 28
	TAG_ERROR            state = 29
	TAG_SUCCESS          state = 30
)

func (s state) String() string {
	return [34]string{
		"DELETING", "DELETE_ERROR", "DELETE_SUCCESS",
		"NONE",
		"CREATING", "CREATE_ERROR", "CREATE_SUCCESS",
		"CLEANING", "CLEAN_ERROR", "CLEAN_SUCCESS",
		"CLONING", "CLONE_ERROR", "CLONE_SUCCESS",
		"PREPARING", "PREPARE_ERROR", "PREPARE_SUCCESS",
		"PULLING", "PULL_ERROR", "PULL_SUCCESS",
		"BUILDING", "BUILD_ERROR", "BUILD_SUCCESS",
		"PREPACKAGING", "PREPACKAGE_ERROR", "PREPACKAGE_SUCCESS",
		"PACKAGING", "PACKAGE_ERROR", "PACKAGE_SUCCESS",
		"PUSHING", "PUSH_ERROR", "PUSH_SUCCESS",
		"TAGGING", "TAG_ERROR", "TAG_SUCCESS",
	}[s+3]
}

type task struct {
	id    int
	kind  string
	state string
	time  string
}

type registry struct {
	id       int
	name     string
	url      string
	user     string
	password string
	login    time.Time
	timeout  int
}

type taskTrigger struct {
	url      string
	branch   string
	commit   string
	tag      string
	registry string
	project  int
	version  int
}

type taskRequest struct {
	state   state
	index   int
	trigger *taskTrigger
}

type credential struct {
	id          int
	description string
	value       string
}

type destination struct {
	registry *registry
	tag      string
}

type trigger struct {
	project *project
	state   state
}

type project struct {
	id             int
	name           string
	labels         string
	url            string
	branch         string
	buildSpec      string
	prepackageSpec string
	packageSpec    string
	buildHash      []byte
	state          state
	version        int
	protected      bool
	tagRepo        bool
	destinations   []destination
	tasks          []*task
	queue          chan taskRequest
	triggers       []trigger
	credentials    map[string]*credential
	prepareDep     *project
	prepackageDep  *project
	packageDep     *project
	commit         string
}

type broker struct {
	events     chan []byte
	register   chan chan []byte
	unregister chan chan []byte
	clients    map[chan []byte]bool
}

var db *sql.DB
var registries = map[int]*registry{}
var credentials = map[int]*credential{}
var projects = map[int]*project{}
var projectAbs, _ = filepath.Abs("projects")
var clients = &broker{
	make(chan []byte),
	make(chan chan []byte),
	make(chan chan []byte),
	make(map[chan []byte]bool),
}
var defaultRequest = taskRequest{NONE, 0, nil}

func event(event map[string]interface{}) {
	bytes, _ := json.Marshal(event)
	clients.events <- bytes
}

func registryList() []map[string]interface{} {
	result := make([]map[string]interface{}, 0)
	for id, r := range registries {
		result = append(result, map[string]interface{}{
			"id":      id,
			"name":    r.name,
			"url":     r.url,
			"user":    r.user,
			"timeout": r.timeout,
		})
	}
	return result
}

func registryCreate(name, url, user, password string, timeout int) *registry {
	var id int
	db.QueryRow(`INSERT INTO registries(name, url, user, password, timeout) VALUES(?, ?, ?, ?, ?) RETURNING id`,
		name, url, user, password, timeout).Scan(&id)
	logger.Infof("Registry created %s %s %s ******", name, url, user)
	r := &registry{id, name, url, user, password, time.Unix(0, 0), timeout}
	registries[r.id] = r
	return r
}

func registryLogin(r *registry) string {
	if time.Since(r.login).Minutes() > float64(r.timeout) {
		if len(r.user) > 0 {
			exec.Command("podman", "login", r.url, "-u", r.user, "-p", r.password).Run()
		}
		r.login = time.Now()
	}
	return r.url
}

func (p *project) buildFrom(state state, trigger taskRequest) {
	p.queue <- taskRequest{state, 0, trigger.trigger}
}

func projectEnvironment(p *project, request taskRequest) string {
	filename := fmt.Sprintf("%s/%d/environment", projectAbs, p.id)
	f, _ := os.Create(filename)
	trigger := request.trigger
	if trigger != nil {
		fmt.Fprintf(f, "RACS_TRIGGER=%s\n", trigger.tag)
		fmt.Fprintf(f, "RACS_VERSION=%d\n", trigger.version)
		fmt.Fprintf(f, "RACS_TRIGGER_URL=%s\n", trigger.url)
		fmt.Fprintf(f, "RACS_TRIGGER_BRANCH=%s\n", trigger.branch)
		fmt.Fprintf(f, "RACS_TRIGGER_COMMIT=%s\n", trigger.commit)
		fmt.Fprintf(f, "RACS_TRIGGER_TAG=%s\n", trigger.tag)
		fmt.Fprintf(f, "RACS_TRIGGER_PROJECT=%d\n", trigger.project)
		fmt.Fprintf(f, "RACS_TRIGGER_REGISTRY=%s\n", trigger.registry)
	}
	for name, cr := range p.credentials {
		fmt.Fprintf(f, "%s=%s\n", name, cr.value)
	}
	f.Close()
	return filename
}

func projectRoutine(p *project) {
	exec.Command("git", "-C", fmt.Sprintf("%s/%d/workspace/source", projectAbs, p.id), "remote", "set-url", "origin", p.url).Output()
	logger.Infof("Project %d waiting for tasks", p.id)
	request := <-p.queue
	for {
		state := request.state
		logger.Infof("Project %d received task %s", p.id, state.String())
		command := ""
		args := []string{}
		switch state {
		case CLEANING:
			command = "rm"
			args = []string{"-rfv", fmt.Sprintf("%s/%d/workspace/source", projectAbs, p.id)}
		case CLONING:
			command = "git"
			args = []string{"clone", "-v", "--recursive", "-b", p.branch, p.url, fmt.Sprintf("%s/%d/workspace/source", projectAbs, p.id)}
		case PREPARING:
			command = "podman"
			spec := fmt.Sprintf("%s/%d/%s", projectAbs, p.id, p.buildSpec)
			args = []string{"build",
				"--pull=newer",
				"--squash",
				"-f", spec,
				"-t", fmt.Sprintf("builder-%d", p.id),
			}
			if p.prepareDep != nil {
				args = append(args, "--from", fmt.Sprintf("package-%d", p.prepareDep.id))
			}
			args = append(args, fmt.Sprintf("%s/%d/context", projectAbs, p.id))
		case PULLING:
			command = "git"
			args = []string{"-C", fmt.Sprintf("%s/%d/workspace/source", projectAbs, p.id), "pull", "--recurse-submodules"}
		case BUILDING:
			command = "podman"
			args = []string{"run", "--network=host", "--rm=true",
				"--env-file", projectEnvironment(p, request),
				"-v", fmt.Sprintf("%s/%d/workspace:/workspace", projectAbs, p.id),
				"--read-only", fmt.Sprintf("builder-%d", p.id),
			}
		case PREPACKAGING:
			if p.prepackageSpec != "" {
				command = "podman"
				spec := fmt.Sprintf("%s/%d/%s", projectAbs, p.id, p.prepackageSpec)
				args = []string{"build",
					"--pull=newer",
					"--layers",
					"--cache-ttl=24h",
					"-f", spec,
					"-t", fmt.Sprintf("prepackage-%d", p.id),
				}
				if p.prepackageDep != nil {
					args = append(args, "--from", fmt.Sprintf("package-%d", p.prepackageDep.id))
				}
				args = append(args, fmt.Sprintf("%s/%d/workspace", projectAbs, p.id))
			} else {
				command = "echo"
				args = []string{"skipping prepackage"}
			}
		case PACKAGING:
			command = "podman"
			spec := fmt.Sprintf("%s/%d/%s", projectAbs, p.id, p.packageSpec)
			args = []string{"build",
				"-v", fmt.Sprintf("%s/%d/workspace:/workspace", projectAbs, p.id),
				"--pull=newer",
				"--squash",
				"-f", spec,
				"-t", fmt.Sprintf("package-%d", p.id),
			}
			if p.packageDep != nil {
				args = append(args, "--from", fmt.Sprintf("package-%d", p.packageDep.id))
			} else if p.prepackageSpec != "" {
				args = append(args, "--from", fmt.Sprintf("prepackage-%d", p.id))
			}
			args = append(args, fmt.Sprintf("%s/%d/context", projectAbs, p.id))
		case PUSHING:
			if request.index < len(p.destinations) {
				destination := p.destinations[request.index]
				url := registryLogin(destination.registry)
				tag := strings.Replace(destination.tag, "$VERSION", strconv.Itoa(p.version), -1)
				command = "podman"
				args = []string{"push", fmt.Sprintf("package-%d", p.id), fmt.Sprintf("%s/%s", url, tag)}
			} else {
				command = "echo"
				args = []string{"skipping push"}
			}
		case TAGGING:
			if p.tagRepo {
				if request.index < len(p.destinations) {
					destination := p.destinations[request.index]
					tag := strings.Replace(destination.tag, "$VERSION", strconv.Itoa(p.version), -1)
					tag = tag[strings.LastIndex(tag, ":")+1:]
					command = "git"
					args = []string{"-C", fmt.Sprintf("%s/%d/workspace/source", projectAbs, p.id), "push", "origin", tag}
				}
			} else {
				command = "echo"
				args = []string{"skipping tag"}
			}
		case DELETING:
			command = "rm"
			args = []string{"-vrf", fmt.Sprintf("%s/%d", projectAbs, p.id)}
		}
		p.state = state
		if len(command) > 0 {
			var id int
			var time string
			err := db.QueryRow(`INSERT INTO tasks(project, type, state, time)
				VALUES(?, ?, 'RUNNING', datetime('now')) RETURNING id, time`, p.id, p.state.String()).Scan(&id, &time)
			if err != nil {
				logger.Fatal(err)
			}
			logger.Infof("Creating task %d:%d", p.id, id)
			t := &task{id, p.state.String(), "RUNNING", time}
			p.tasks = append(p.tasks, t)
			if len(p.tasks) > 5 {
				p.tasks = p.tasks[1:]
			}
			event(map[string]interface{}{
				"event":   "task/create",
				"project": p.id,
				"id":      t.id,
				"type":    t.kind,
				"time":    t.time,
				"state":   "RUNNING",
			})
			taskRoot := fmt.Sprintf("tasks/%d", t.id)
			os.Mkdir(taskRoot, 0777)
			logger.Infof("Task %s %v", command, args)
			cmd := exec.Command(command, args...)
			out, _ := os.Create(fmt.Sprintf("%s/out.log", taskRoot))
			out.WriteString("\u001B[1m")
			out.WriteString(cmd.String())
			out.WriteString("\u001B[0m\n")
			cmd.Stdout = out
			cmd.Stderr = out
			err = cmd.Run()
			if err != nil {
				t.state = "ERROR"
				p.state += 1
			} else {
				t.state = "SUCCESS"
				p.state += 2
			}
			out.Close()
			logger.Infof("Task %d completed", t.id)
			db.Exec(`UPDATE projects SET state = ? WHERE id = ?`, p.state.String(), p.id)
			db.Exec(`UPDATE tasks SET state = ? WHERE id = ?`, t.state, t.id)
			event(map[string]interface{}{
				"event": "project/state",
				"id":    p.id,
				"state": p.state.String(),
			})
			event(map[string]interface{}{
				"event":   "task/state",
				"project": p.id,
				"id":      t.id,
				"state":   t.state,
			})
		}
		logger.Infof("Project %d finished task %s", p.id, state.String())
		switch p.state {
		case CREATE_SUCCESS:
			request = taskRequest{CLEANING, 0, request.trigger}
		case CLEAN_SUCCESS:
			request = taskRequest{CLONING, 0, request.trigger}
		case CLONE_SUCCESS:
			request = taskRequest{PREPARING, 0, request.trigger}
		case PREPARE_SUCCESS:
			request = taskRequest{PULLING, 0, request.trigger}
		case PULL_SUCCESS:
			buildHash := []byte{}
			f, err := os.Open(fmt.Sprintf("%s/%d/%s", projectAbs, p.id, p.buildSpec))
			if err == nil {
				h := sha256.New()
				io.Copy(h, f)
				f.Close()
				buildHash = h.Sum(nil)
			} else {
				logger.Warn(err)
			}
			if !bytes.Equal(buildHash, p.buildHash) {
				p.buildHash = buildHash
				db.Exec(`UPDATE projects SET buildHash = ? WHERE id = ?`, buildHash, p.id)
				request = taskRequest{PREPARING, 0, request.trigger}
			} else {
				request = taskRequest{BUILDING, 0, request.trigger}
			}
		case BUILD_SUCCESS:
			out, err := exec.Command("git", "-C", fmt.Sprintf("%s/%d/workspace/source", projectAbs, p.id), "rev-parse", "HEAD").Output()
			if err == nil {
				p.commit = strings.TrimSpace(string(out))
			}
			request = taskRequest{PREPACKAGING, 0, request.trigger}
		case PREPACKAGING_SUCCESS:
			request = taskRequest{PACKAGING, 0, request.trigger}
		case PACKAGE_SUCCESS:
			p.version += 1
			db.Exec(`UPDATE projects SET version = ? WHERE id = ?`, p.version, p.id)
			event(map[string]interface{}{
				"event":   "project/version",
				"id":      p.id,
				"version": p.version,
			})
			_, err := exec.Command("git", "-C", fmt.Sprintf("%s/%d/workspace/source", projectAbs, p.id), "tag", fmt.Sprintf("r%d", p.version)).Output()
			if err != nil {
				logger.Error(err)
			}
			request = taskRequest{PUSHING, 0, request.trigger}
		case PUSH_SUCCESS:
			index := request.index
			if len(p.triggers) > 0 {
				tag := ""
				registry := ""
				if index < len(p.destinations) {
					destination := p.destinations[index]
					tag = strings.Replace(destination.tag, "$VERSION", strconv.Itoa(p.version), -1)
					registry = destination.registry.name
				}
				request2 := taskRequest{state, 0, &taskTrigger{p.url, p.branch, p.commit, tag, registry, p.id, p.version}}
				for _, trigger := range p.triggers {
					trigger.project.buildFrom(trigger.state, request2)
				}
			}
			index = index + 1
			if index < len(p.destinations) {
				request = taskRequest{PUSHING, index, request.trigger}
			} else {
				request = taskRequest{TAGGING, 0, request.trigger}
			}
		case TAG_SUCCESS:
			index := request.index + 1
			if index < len(p.destinations) {
				request = taskRequest{TAGGING, index, request.trigger}
			} else {
				request = <-p.queue
			}
		case DELETE_SUCCESS:
			db.Exec(`DELETE FROM projects WHERE id = ?`, p.id)
			db.Exec(`DELETE FROM tasks WHERE project = ?`, p.id)
			delete(projects, p.id)
			return
		default:
			request = <-p.queue
		}
	}
}

func projectCreate(name, url, branch, labels string) *project {
	var id int
	db.QueryRow(`INSERT INTO projects(name, source, branch, labels, buildSpec, prepackageSpec, packageSpec, state, version, protected, tagRepo)
		VALUES(?, ?, ?, ?, 'BuildSpec', '', 'PackageSpec', 'CLONING', 0, 0, 0) RETURNING id`, name, url, branch, labels).Scan(&id)
	logger.Infof("Project created %s %s %s %s", id, name, url, branch)
	os.Mkdir(fmt.Sprintf("%s/%d", projectAbs, id), 0777)
	os.Mkdir(fmt.Sprintf("%s/%d/context", projectAbs, id), 0777)
	os.Mkdir(fmt.Sprintf("%s/%d/workspace", projectAbs, id), 0777)
	p := &project{
		id, name, labels, url, branch, "BuildSpec", "", "PackageSpec", []byte{},
		CREATE_SUCCESS, 0, false, false,
		make([]destination, 0),
		make([]*task, 0),
		make(chan taskRequest, 10),
		make([]trigger, 0),
		make(map[string]*credential),
		nil, nil, nil, "",
	}
	projects[p.id] = p
	go projectRoutine(p)
	event(map[string]interface{}{
		"event":          "project/create",
		"id":             p.id,
		"name":           p.name,
		"labels":         p.labels,
		"url":            p.url,
		"branch":         p.branch,
		"buildSpec":      p.buildSpec,
		"prepackageSpec": p.prepackageSpec,
		"packageSpec":    p.packageSpec,
		"state":          p.state.String(),
		"version":        p.version,
		"protected":      p.protected,
		"tagRepo":        p.tagRepo,
	})
	return p
}

var staticPath, _ = filepath.Abs("static")

func loadStatic(path string) ([]byte, error) {
	path = filepath.Clean(path)
	if path == "." {
		return nil, errors.New("Not found")
	}
	return ioutil.ReadFile(staticPath + path)
}

func projectList() []map[string]interface{} {
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
		destinations := make([]interface{}, 0)
		for _, destination := range p.destinations {
			destinations = append(destinations, []interface{}{
				destination.registry.id, destination.tag,
			})
		}
		triggers := make([]interface{}, 0)
		for _, trigger := range p.triggers {
			triggers = append(triggers, []interface{}{
				trigger.project.id, trigger.state.String(),
			})
		}
		environment := make([]interface{}, 0)
		for name, credential := range p.credentials {
			environment = append(environment, []interface{}{
				name, credential.id, credential.description,
			})
		}
		result = append(result, map[string]interface{}{
			"id":             id,
			"name":           p.name,
			"labels":         p.labels,
			"url":            p.url,
			"branch":         p.branch,
			"destinations":   destinations,
			"buildSpec":      p.buildSpec,
			"prepackageSpec": p.prepackageSpec,
			"packageSpec":    p.packageSpec,
			"state":          p.state.String(),
			"tasks":          tasks,
			"version":        p.version,
			"protected":      p.protected,
			"tagRepo":        p.tagRepo,
			"triggers":       triggers,
			"environment":    environment,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i]["id"].(int) < result[j]["id"].(int)
	})
	return result
}

var ciph cipher.Block

type user struct {
	Name  string
	Roles []string
}

func renderLogin(w http.ResponseWriter, path string, params map[string]string) {
	loginTemplate, _ := template.ParseFiles(staticPath + "/login.xhtml")
	w.Header().Add("Content-Type", "application/xhtml+xml")
	var sb strings.Builder
	sep := ""
	for name, value := range params {
		sb.WriteString(sep)
		sb.WriteString(url.QueryEscape(name))
		sb.WriteRune('=')
		sb.WriteString(url.QueryEscape(value))
		sep = "&"
	}
	err := loginTemplate.Execute(w, map[string]interface{}{
		"action": path,
		"params": sb.String(),
	})
	if err != nil {
		logger.Error(err)
	}
}

func renderDenied(w http.ResponseWriter, path string, params map[string]string) {

}

var noLogin bool = false

func checkLogin(u *user, role string, w http.ResponseWriter, path string, params map[string]string) bool {
	if noLogin {
		return false
	}
	for _, r := range u.Roles {
		if r == role {
			return false
		}
	}
	renderLogin(w, path, params)
	return true
}

func handleEvents(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	events := make(chan []byte)
	clients.register <- events
	defer func() {
		clients.unregister <- events
	}()
	notify := w.(http.CloseNotifier).CloseNotify()
	go func() {
		<-notify
		clients.unregister <- events
	}()
	j, _ := json.Marshal(map[string]interface{}{
		"event":    "project/list",
		"projects": projectList(),
	})
	fmt.Fprintf(w, "data: %s\n\n", j)
	flusher.Flush()
	for {
		fmt.Fprintf(w, "data: %s\n\n", <-events)
		flusher.Flush()
	}
}

func handleUserLogin(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	username := params["username"]
	password := params["password"]
	tr, err := pam.StartFunc("sudo", username, func(s pam.Style, msg string) (string, error) {
		switch s {
		case pam.PromptEchoOn:
			return username, nil
		case pam.PromptEchoOff:
			return password, nil
		}
		return "", errors.New("Unrecognized message")
	})
	if err != nil {
		logger.Error(err)
	}
	err = tr.SetItem(pam.Ruser, username)
	if err != nil {
		logger.Error(err)
	}
	err = tr.Authenticate(0)
	if err != nil {
		logger.Error(err)
		w.WriteHeader(401)
		w.Write([]byte(err.Error()))
		return
	}
	u2 := user{username, []string{"admin", "user"}}
	gcm, _ := cipher.NewGCM(ciph)
	nonceSize := gcm.NonceSize()
	nonce := make([]byte, nonceSize)
	rand.Read(nonce)
	in, _ := json.Marshal(u2)
	en := gcm.Seal(nil, nonce, in, nil)
	out := make([]byte, len(en)+nonceSize)
	copy(out[:nonceSize], nonce)
	copy(out[nonceSize:], en)
	cookie := http.Cookie{
		Name:    "RACS_TOKEN",
		Value:   hex.EncodeToString(out),
		Path:    "/",
		Expires: time.Now().Add(24 * time.Hour),
	}
	http.SetCookie(w, &cookie)
	action := params["action"]
	redirect := params["redirect"]
	if len(action) > 0 {
		query, _ := url.ParseQuery(params["params"])
		params := make(map[string]string)
		for name, values := range query {
			params[name] = values[0]
		}
		handleAction(action, w, r, &u2, params)
	} else if len(redirect) > 0 {
		w.Header().Add("Location", redirect)
		w.WriteHeader(303)
	} else {
		w.WriteHeader(200)
		w.Write([]byte(username))
	}
}

func handleUserLogout(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	cookie := http.Cookie{
		Name:    "RACS_TOKEN",
		Value:   "",
		Path:    "/",
		Expires: time.Unix(0, 0),
	}
	http.SetCookie(w, &cookie)
	redirect := params["redirect"]
	if len(redirect) > 0 {
		w.Header().Add("Location", redirect)
		w.WriteHeader(303)
	} else {
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	}
}

func handleUserCurrent(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	w.WriteHeader(200)
	w.Write([]byte(u.Name))
}

func handleProjectList(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	result := projectList()
	w.Header().Add("Content-Type", "application/json")
	j, _ := json.Marshal(result)
	w.Write(j)
}

func handleProjectStatus(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	id, _ := strconv.Atoi(params["id"])
	p := projects[id]
	if p == nil {
		w.WriteHeader(500)
	} else {
		w.Header().Add("Content-Type", "application/json")
		j, _ := json.Marshal(map[string]interface{}{
			"id":     id,
			"name":   p.name,
			"url":    p.url,
			"branch": p.branch,
			//"destination":    p.destination,
			"buildSpec":      p.buildSpec,
			"prepackageSpec": p.prepackageSpec,
			"packageSpec":    p.packageSpec,
			//"tag":            p.tag,
			"labels": p.labels,
		})
		w.Write(j)
	}
}

func projectUpdateEvent(p *project) {
	destinations := make([]interface{}, 0)
	for _, destination := range p.destinations {
		destinations = append(destinations, []interface{}{
			destination.registry.id, destination.tag,
		})
	}
	triggers := make([]interface{}, 0)
	for _, trigger := range p.triggers {
		triggers = append(triggers, []interface{}{
			trigger.project.id, trigger.state.String(),
		})
	}
	environment := make([]interface{}, 0)
	for name, credential := range p.credentials {
		environment = append(environment, []interface{}{
			name, credential.id, credential.description,
		})
	}
	event(map[string]interface{}{
		"event":          "project/update",
		"id":             p.id,
		"name":           p.name,
		"labels":         p.labels,
		"url":            p.url,
		"branch":         p.branch,
		"destinations":   destinations,
		"buildSpec":      p.buildSpec,
		"prepackageSpec": p.prepackageSpec,
		"packageSpec":    p.packageSpec,
		"protected":      p.protected,
		"tagRepo":        p.tagRepo,
		"triggers":       triggers,
		"environment":    environment,
	})
}

func handleProjectUpdate(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	if checkLogin(u, "admin", w, "/project/update", params) {
		return
	}
	id, _ := strconv.Atoi(params["id"])
	p := projects[id]
	if p == nil {
		w.WriteHeader(500)
	} else {
		p.name = params["name"]
		p.labels = params["labels"]
		p.url = params["url"]
		p.branch = params["branch"]
		if params["buildSpec"] != "" {
			p.buildSpec = filepath.Clean(params["buildSpec"])
		} else {
			p.buildSpec = ""
		}
		if params["prepackageSpec"] != "" {
			p.prepackageSpec = filepath.Clean(params["prepackageSpec"])
		} else {
			p.prepackageSpec = ""
		}
		if params["packageSpec"] != "" {
			p.packageSpec = filepath.Clean(params["packageSpec"])
		} else {
			p.packageSpec = ""
		}
		p.protected = params["protected"] != ""
		p.tagRepo = params["tagRepo"] != ""
		db.Exec(`UPDATE projects SET name = ?, labels = ?, source = ?, branch = ?, buildSpec = ?, prepackageSpec = ?, packageSpec = ?, protected = ?, tagRepo = ? WHERE id = ?`,
			p.name, p.labels, p.url, p.branch, p.buildSpec, p.prepackageSpec, p.packageSpec, p.protected, p.tagRepo, p.id)
		projectUpdateEvent(p)
		exec.Command("git", "-C", fmt.Sprintf("%s/%d/workspace/source", projectAbs, p.id), "remote", "set-url", "origin", p.url).Output()
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

func handleProjectCreate(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	if checkLogin(u, "admin", w, "/project/create", params) {
		return
	}
	name := params["name"]
	url := params["url"]
	branch := params["branch"]
	labels := params["labels"]
	p := projectCreate(name, url, branch, labels)
	redirect := params["redirect"]
	if len(redirect) > 0 {
		w.Header().Add("Location", redirect)
		w.WriteHeader(303)
	} else {
		w.WriteHeader(201)
		w.Write([]byte(strconv.Itoa(p.id)))
	}
}

func handleProjectUpload(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	if r.MultipartForm != nil {
		files := r.MultipartForm.File["file"]
		if (files != nil) && (len(files) > 0) {
			file := files[0]
			temp, _ := ioutil.TempFile("uploads", "upload-")
			rd, _ := file.Open()
			io.Copy(temp, rd)
			temp.Close()
			rd.Close()
			params["upload"] = temp.Name()
		}
	}
	if params["value"] != "" {
		temp, _ := ioutil.TempFile("uploads", "upload-")
		temp.WriteString(params["value"])
		temp.Close()
		params["upload"] = temp.Name()
	}
	if checkLogin(u, "admin", w, "/project/upload", params) {
		return
	}
	id, _ := strconv.Atoi(params["id"])
	name := filepath.Clean(params["name"])
	upload := filepath.Clean(params["upload"])
	validUpload, _ := regexp.MatchString("^uploads/upload-[0-9]+$", upload)
	p := projects[id]
	if p == nil {
		w.WriteHeader(500)
	} else if name == "." {
		w.WriteHeader(500)
	} else if !validUpload {
		w.WriteHeader(500)
	} else {
		err := os.Rename(upload, fmt.Sprintf("%s/%d/%s", projectAbs, id, name))
		if err != nil {
			logger.Error(err)
		}
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

func handleProjectDestinations(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	if checkLogin(u, "admin", w, "/project/destinations", params) {
		return
	}
	pid, _ := strconv.Atoi(params["id"])
	p := projects[pid]
	p.destinations = make([]destination, 0)
	db.Exec(`DELETE FROM destinations WHERE project = ?`, p.id)
	destinations := strings.FieldsFunc(params["destinations"], func(c rune) bool {
		return c == ','
	})
	for i := 0; i < len(destinations); i += 2 {
		rid, _ := strconv.Atoi(destinations[i])
		r := registries[rid]
		tag := destinations[i+1]
		p.destinations = append(p.destinations, destination{r, tag})
		db.Exec(`INSERT INTO destinations(project, registry, tag) VALUES(?, ?, ?)`, p.id, r.id, tag)
	}
	projectUpdateEvent(p)
	redirect := params["redirect"]
	if len(redirect) > 0 {
		w.Header().Add("Location", redirect)
		w.WriteHeader(303)
	} else {
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	}
}

func handleProjectTriggers(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	if checkLogin(u, "admin", w, "/project/triggers", params) {
		return
	}
	pid, _ := strconv.Atoi(params["id"])
	p := projects[pid]
	for _, trigger := range p.triggers {
		switch trigger.state {
		case PREPARING:
			trigger.project.prepareDep = nil
		case PREPACKAGING:
			trigger.project.prepackageDep = nil
		case PACKAGING:
			trigger.project.packageDep = nil
		}
	}
	p.triggers = make([]trigger, 0)
	db.Exec(`DELETE FROM triggers WHERE project = ?`, p.id)
	triggers := strings.FieldsFunc(params["triggers"], func(c rune) bool {
		return c == ','
	})
	for i := 0; i < len(triggers); i += 2 {
		tid, _ := strconv.Atoi(triggers[i])
		t := projects[tid]
		s := NONE
		switch triggers[i+1] {
		case "clean":
			s = CLEANING
		case "clone":
			s = CLONING
		case "prepare":
			s = PREPARING
			t.prepareDep = p
		case "pull":
			s = PULLING
		case "build":
			s = BUILDING
		case "prepackage":
			s = PREPACKAGING
			t.prepackageDep = p
		case "package":
			s = PACKAGING
			t.packageDep = p
		case "push":
			s = PUSHING
		case "tag":
			s = TAGGING
		}
		p.triggers = append(p.triggers, trigger{t, s})
		db.Exec(`INSERT INTO triggers(project, target, state) VALUES(?, ?, ?)`, p.id, t.id, s.String())
	}
	projectUpdateEvent(p)
	redirect := params["redirect"]
	if len(redirect) > 0 {
		w.Header().Add("Location", redirect)
		w.WriteHeader(303)
	} else {
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	}
}

func handleProjectEnvironment(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	if checkLogin(u, "admin", w, "/project/environment", params) {
		return
	}
	pid, _ := strconv.Atoi(params["id"])
	p := projects[pid]
	p.credentials = make(map[string]*credential)
	db.Exec(`DELETE FROM environments WHERE project = ?`, p.id)
	environment := strings.FieldsFunc(params["environment"], func(c rune) bool {
		return c == ','
	})
	for i := 0; i < len(environment); i += 2 {
		name := environment[i]
		crid, _ := strconv.Atoi(environment[i+1])
		cr := credentials[crid]
		if cr != nil {
			p.credentials[name] = cr
			db.Exec(`INSERT INTO environments(project, name, credential) VALUES(?, ?, ?)`, p.id, name, cr.id)
		}
	}
	projectUpdateEvent(p)
	redirect := params["redirect"]
	if len(redirect) > 0 {
		w.Header().Add("Location", redirect)
		w.WriteHeader(303)
	} else {
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	}
}

func handleProjectBuild(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	id, _ := strconv.Atoi(params["id"])
	stage := params["stage"]
	p := projects[id]
	if p.protected && u.Name == "" {
		w.WriteHeader(403)
		w.Write([]byte("Unauthorized"))
		return
	}
	expectedRef := fmt.Sprintf("refs/heads/%s", p.branch)
	requestedRef := expectedRef
	if params["payload"] != "" {
		var j map[string]interface{}
		json.Unmarshal([]byte(params["payload"]), &j)
		requestedRef = fmt.Sprint(j["ref"])
	}
	if requestedRef == expectedRef {
		switch stage {
		case "clean":
			p.buildFrom(CLEANING, defaultRequest)
		case "clone":
			p.buildFrom(CLONING, defaultRequest)
		case "prepare":
			p.buildFrom(PREPARING, defaultRequest)
		case "pull":
			p.buildFrom(PULLING, defaultRequest)
		case "build":
			p.buildFrom(BUILDING, defaultRequest)
		case "prepackage":
			p.buildFrom(PREPACKAGING, defaultRequest)
		case "package":
			p.buildFrom(PACKAGING, defaultRequest)
		case "push":
			p.buildFrom(PUSHING, defaultRequest)
		case "tag":
			p.buildFrom(TAGGING, defaultRequest)
		}
	} else {
		logger.Infof("Build requested by %s expected %s, skipping", requestedRef, expectedRef)
	}
	w.WriteHeader(200)
	w.Write([]byte("OK"))
}

func handleProjectDelete(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	if checkLogin(u, "admin", w, "/project/delete", params) {
		return
	}
	id, _ := strconv.Atoi(params["id"])
	confirm := params["confirm"]
	if confirm == "YES" {
		projects[id].buildFrom(DELETING, defaultRequest)
	}
	redirect := params["redirect"]
	if len(redirect) > 0 {
		w.Header().Add("Location", redirect)
		w.WriteHeader(303)
	} else {
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	}
}

func handleTaskList(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	from, _ := strconv.ParseInt(params["from"], 10, 64)
	rows, _ := db.Query(`SELECT project, id, type, state, time FROM tasks ORDER BY id DESC LIMIT 100 OFFSET ?`, from)
	result := make([]interface{}, 0)
	for rows.Next() {
		var pid int
		var id int
		var kind string
		var state string
		var time string
		rows.Scan(&pid, &id, &kind, &state, &time)
		result = append(result, map[string]interface{}{
			"project": pid,
			"id":      id,
			"type":    kind,
			"state":   state,
			"time":    time,
		})
	}
	w.Header().Add("Content-Type", "application/json")
	j, _ := json.Marshal(result)
	w.Write(j)
}

func handleTaskLogs(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	id, _ := strconv.Atoi(params["id"])
	var state string
	db.QueryRow(`SELECT state FROM tasks WHERE id = ?`, id).Scan(&state)
	offset, _ := strconv.ParseInt(params["offset"], 10, 64)
	file, _ := os.Open(fmt.Sprintf("tasks/%d/out.log", id))
	file.Seek(offset, 0)
	bytes, _ := ioutil.ReadAll(file)
	w.Header().Add("Content-Type", "text/plain")
	w.Header().Add("X-Task-State", state)
	w.Write(bytes)
}

func handleRegistryList(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	result := registryList()
	w.Header().Add("Content-Type", "application/json")
	j, _ := json.Marshal(result)
	w.Write(j)
}

func handleRegistryCreate(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	if checkLogin(u, "admin", w, "/registry/create", params) {
		return
	}
	name := params["name"]
	url := params["url"]
	user := params["user"]
	password := params["password"]
	timeout, _ := strconv.Atoi(params["timeout"])
	reg := registryCreate(name, url, user, password, timeout)
	redirect := params["redirect"]
	if len(redirect) > 0 {
		w.Header().Add("Location", redirect)
		w.WriteHeader(303)
	} else {
		w.WriteHeader(201)
		w.Write([]byte(reg.name))
	}
}

func handleRegistryUpdate(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	if checkLogin(u, "admin", w, "/registry/update", params) {
		return
	}
	id, _ := strconv.Atoi(params["id"])
	reg := registries[id]
	reg.name = params["name"]
	reg.url = params["url"]
	reg.user = params["user"]
	reg.password = params["password"]
	reg.timeout, _ = strconv.Atoi(params["timeout"])
	db.Exec(`UPDATE registries SET name = ?, url = ?, user = ?, password = ?, timeout = ? WHERE id = ?`, reg.name, reg.url, reg.user, reg.password, reg.timeout, reg.id)
	redirect := params["redirect"]
	if len(redirect) > 0 {
		w.Header().Add("Location", redirect)
		w.WriteHeader(303)
	} else {
		w.WriteHeader(201)
		w.Write([]byte(reg.name))
	}
}

func handleCredentialList(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	result := make([]map[string]interface{}, 0)
	for id, cr := range credentials {
		result = append(result, map[string]interface{}{
			"id":          id,
			"description": cr.description,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		adesc := result[i]["description"].(string)
		bdesc := result[j]["description"].(string)
		return adesc < bdesc
	})
	w.Header().Add("Content-Type", "application/json")
	j, _ := json.Marshal(result)
	w.Write(j)
}

func handleCredentialCreate(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	if checkLogin(u, "admin", w, "/credential/create", params) {
		return
	}
	description := params["description"]
	value := params["value"]
	var id int
	db.QueryRow(`INSERT INTO credentials(description, value) VALUES(?, ?) RETURNING id`, description, value).Scan(&id)
	credentials[id] = &credential{id, description, value}
	redirect := params["redirect"]
	if len(redirect) > 0 {
		w.Header().Add("Location", redirect)
		w.WriteHeader(303)
	} else {
		w.WriteHeader(201)
		w.Write([]byte(strconv.Itoa(id)))
	}
}

func handleCredentialUpdate(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
	if checkLogin(u, "admin", w, "/credential/update", params) {
		return
	}
	id, _ := strconv.Atoi(params["id"])
	value := params["value"]
	cr := credentials[id]
	cr.value = value
	db.Exec(`UPDATE credentials SET value = ? WHERE id = ?`, value, id)
	redirect := params["redirect"]
	if len(redirect) > 0 {
		w.Header().Add("Location", redirect)
		w.WriteHeader(303)
	} else {
		w.WriteHeader(200)
		w.Write([]byte("OK"))
	}
}

func handleCredentialDelete(w http.ResponseWriter, r *http.Request, u *user, params map[string]string) {
}

type handler func(w http.ResponseWriter, r *http.Request, u *user, params map[string]string)

var handlers = map[string]handler{}

func handleAction(path string, w http.ResponseWriter, r *http.Request, u *user, params map[string]string) bool {
	handler := handlers[path]
	if handler != nil {
		handler(w, r, u, params)
		return true
	} else {
		return false
	}
}

func handleRoot(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	logger.Infof("%s %s %s", r.Method, r.RemoteAddr, path)
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
	u := user{"", []string{}}
	if noLogin {
		u.Name = "user"
	}
	cookie, err := r.Cookie("RACS_TOKEN")
	if cookie != nil {
		b, _ := hex.DecodeString(cookie.Value)
		gcm, _ := cipher.NewGCM(ciph)
		nonceSize := gcm.NonceSize()
		nonce, in := b[:nonceSize], b[nonceSize:]
		de, _ := gcm.Open(nil, nonce, in, nil)
		json.Unmarshal(de, &u)
	}
	if handleAction(path, w, r, &u, params) {
		return
	}
	if path == "/" {
		path = "/index.xhtml"
	}
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

func main() {
	var sslCert, sslKey string
	var port int
	flag.StringVar(&sslCert, "ssl-cert", "", "SSL cert")
	flag.StringVar(&sslKey, "ssl-key", "", "SSL key")
	flag.BoolVar(&noLogin, "no-login", false, "Allow all actions without login")
	flag.IntVar(&port, "port", 8080, "Web server port")
	flag.Parse()

	key := make([]byte, 32)
	rand.Read(key)
	ciph, _ = aes.NewCipher(key)

	var err error

	os.Mkdir("projects", 0777)
	os.Mkdir("tasks", 0777)
	os.Mkdir("uploads", 0777)
	os.Setenv("GIT_TERMINAL_PROMPT", "0")

	db, err = sql.Open("sqlite3", "file:main.db?cache=shared")
	if err != nil {
		logger.Fatal(err)
		os.Exit(-1)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)

	var version int
	err = db.QueryRow(`SELECT value FROM config WHERE name = 'version'`).Scan(&version)
	if err != nil {
		bytes, _ := ioutil.ReadFile("schemas/current.sql")
		stats := strings.Split(string(bytes), ";")
		for _, stat := range stats {
			_, err := db.Exec(stat)
			if err != nil {
				logger.Fatal(err)
				os.Exit(-1)
			}
		}
	} else {
		for {
			bytes, err := ioutil.ReadFile(fmt.Sprintf("schemas/upgrade-%d.sql", version))
			if err != nil {
				break
			}
			stats := strings.Split(string(bytes), ";")
			for _, stat := range stats {
				stat = strings.TrimSpace(stat)
				if len(stat) > 0 {
					logger.Infof("Executing upgrade SQL: %s", stat)
					_, err := db.Exec(stat)
					if err != nil {
						logger.Fatal(err)
						os.Exit(-1)
					}
				}
			}
			version += 1
		}
	}

	states := make(map[string]state)
	for state := DELETING; state <= TAG_SUCCESS; state += 1 {
		states[state.String()] = state
	}
	rows, err := db.Query(`SELECT id, name, url, user, password, timeout FROM registries`)
	for rows.Next() {
		var id int
		var name string
		var url string
		var user string
		var password string
		var timeout int
		rows.Scan(&id, &name, &url, &user, &password, &timeout)
		registries[id] = &registry{id, name, url, user, password, time.Unix(0, 0), timeout}
	}
	rows, err = db.Query(`SELECT id, description, value FROM credentials`)
	for rows.Next() {
		var id int
		var description string
		var value string
		rows.Scan(&id, &description, &value)
		cr := &credential{id, description, value}
		credentials[cr.id] = cr
	}
	rows, err = db.Query(`SELECT id, name, labels, source, branch, buildSpec, prepackageSpec, packageSpec, buildHash, state, version, protected, tagRepo FROM projects`)
	for rows.Next() {
		var id int
		var name string
		var source string
		var branch string
		var buildSpec string
		var prepackageSpec string
		var packageSpec string
		var buildHash []byte
		var labels string
		var stateName string
		var version int
		var protected int
		var tagRepo int
		err := rows.Scan(&id, &name, &labels, &source, &branch, &buildSpec, &prepackageSpec, &packageSpec, &buildHash, &stateName, &version, &protected, &tagRepo)
		if err != nil {
			logger.Error(err)
		}
		p := &project{
			id, name, labels, source, branch, buildSpec, prepackageSpec, packageSpec, buildHash,
			states[stateName], version, protected == 1, tagRepo == 1,
			make([]destination, 0),
			make([]*task, 0),
			make(chan taskRequest, 10),
			make([]trigger, 0),
			make(map[string]*credential),
			nil, nil, nil, "",
		}
		out, err := exec.Command("git", "-C", fmt.Sprintf("%s/%d/workspace/source", projectAbs, p.id), "rev-parse", "HEAD").Output()
		if err == nil {
			p.commit = strings.TrimSpace(string(out))
		}
		fmt.Printf("%+v\n", p)
		projects[p.id] = p
		go projectRoutine(p)
	}
	rows, err = db.Query(`SELECT project, registry, tag FROM destinations`)
	for rows.Next() {
		var pid int
		var rid int
		var tag string
		err := rows.Scan(&pid, &rid, &tag)
		if err != nil {
			logger.Error(err)
		}
		p := projects[pid]
		r := registries[rid]
		if p != nil && r != nil {
			p.destinations = append(p.destinations, destination{r, tag})
		}
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
	rows, err = db.Query(`SELECT project, target, state FROM triggers`)
	for rows.Next() {
		var pid int
		var tid int
		var stateName string
		rows.Scan(&pid, &tid, &stateName)
		p := projects[pid]
		t := projects[tid]
		if p != nil && t != nil {
			p.triggers = append(p.triggers, trigger{t, states[stateName]})
			switch states[stateName] {
			case PREPARING:
				t.prepareDep = p
			case PREPACKAGING:
				t.prepackageDep = p
			case PACKAGING:
				t.packageDep = p
			}
		}
	}
	rows, err = db.Query(`SELECT project, name, credential FROM environments`)
	for rows.Next() {
		var pid int
		var name string
		var crid int
		rows.Scan(&pid, &name, &crid)
		p := projects[pid]
		cr := credentials[crid]
		if p != nil && cr != nil {
			p.credentials[name] = cr
		}
	}

	go func() {
		for {
			select {
			case client := <-clients.register:
				clients.clients[client] = true
			case client := <-clients.unregister:
				delete(clients.clients, client)
			case event := <-clients.events:
				for client, _ := range clients.clients {
					client <- event
				}
			}
		}
	}()

	go func() {
		for {
			logger.Info("Pruning images")
			err := exec.Command("podman", "image", "prune", "-f", "--filter", "until=5m").Run()
			if err != nil {
				logger.Error(err)
			}
			time.Sleep(60 * time.Second)
		}
	}()

	handlers["/events"] = handleEvents
	handlers["/user/current"] = handleUserCurrent
	handlers["/user/login"] = handleUserLogin
	handlers["/user/logout"] = handleUserLogout
	handlers["/project/list"] = handleProjectList
	handlers["/project/status"] = handleProjectStatus
	handlers["/project/update"] = handleProjectUpdate
	handlers["/project/destinations"] = handleProjectDestinations
	handlers["/project/triggers"] = handleProjectTriggers
	handlers["/project/environment"] = handleProjectEnvironment
	handlers["/project/create"] = handleProjectCreate
	handlers["/project/upload"] = handleProjectUpload
	handlers["/project/build"] = handleProjectBuild
	handlers["/project/delete"] = handleProjectDelete
	handlers["/task/list"] = handleTaskList
	handlers["/task/logs"] = handleTaskLogs
	handlers["/registry/list"] = handleRegistryList
	handlers["/registry/create"] = handleRegistryCreate
	handlers["/registry/update"] = handleRegistryUpdate
	handlers["/credential/list"] = handleCredentialList
	handlers["/credential/create"] = handleCredentialCreate
	handlers["/credential/update"] = handleCredentialUpdate
	handlers["/credential/delete"] = handleCredentialDelete

	http.HandleFunc("/", handleRoot)
	endpoint := fmt.Sprintf(":%d", port)
	if len(sslCert) > 0 {
		logger.Infof("Listening on https://0.0.0.0:%d", port)
		logger.Fatal(http.ListenAndServeTLS(endpoint, sslCert, sslKey, nil))
	} else {
		logger.Infof("Listening on http://0.0.0.0:%d", port)
		logger.Fatal(http.ListenAndServe(endpoint, nil))
	}
}
