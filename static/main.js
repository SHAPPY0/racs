let Sidebar = {
	view: function() {
		return m("aside.menu", [
			m("p.menu-label", "Projects"),
			m("ul.menu-list", [
				m("li", m(m.route.Link, {href: "/projects"}, "List")),
				m("li", m(m.route.Link, {href: "/project/create"}, "Create"))
			])
		])
	}
}

var projects = [];

function projectsList(vnode) {
	return m.request({url: "/project/list"}).then(function(result) {
		projects = result;
	});
}

function projectBuild(project, event) {
	return m.request({method: "POST", url: "/project/build", body: {id: project.id, stage: event.target.value}}).then(function(result) {
	});
}

var interval;

let Projects = {
	oninit: projectsList,
	oncreate: function(vnode) {
		interval = setInterval(projectsList, 2000, vnode);
	},
	onremove: function(vnode) {
		clearInterval(interval);
	},
	view: function(vnode) {
		return m("div.columns", [
			m("div.column", [m(Sidebar)]),
			m("div.column", [
				m("table.table", [
					m("thead", [
						m("tr", [
							m("th", "Id"),
							m("th", "Name"),
							m("th", "State"),
							m("th", "Task"),
							m("th", "Version"),
							m("th", "Actions")
						])
					]),
					m("tbody", projects.map(function(project) {
						let task = (project.task || "").toString();
						return m("tr", {key: project.id}, [
							m("td", project.id.toString()),
							m("td", project.name.toString()),
							m("td", project.state),
							m("td", [
								m("table.table", project.tasks.map(function(task) {
									return m("tr", [
										m("td", m(m.route.Link, {href: "/task/" + task.id}, task.id)),
										m("td", task.type),
										m("td", task.state)
									])
								}))
							]),
							m("td", project.version.toString()),
							m("td", m(m.route.Link, {href: "/project/upload/" + project.id}, "Upload")),
							m("td", [
								m("select", {onchange: projectBuild.bind(null, project)}, [
									m("option", {}, ""),
									m("option", {value: "clean"}, "Clean"),
									m("option", {value: "prepare"}, "Prepare"),
									m("option", {value: "pull"}, "Pull"),
									m("option", {value: "build"}, "Build"),
									m("option", {value: "package"}, "Package")
								])
							])
						]);				
					}))
				])
			])
		])
	}
}

let ProjectCreate = {
	view: function(vnode) {
		return m("div.columns", [
			m("div.column", [m(Sidebar)]),
			m("div.column", [
				m("form", {onsubmit: function(e) {
					e.preventDefault()
					console.log(e)
					m.request({method: "POST", url: "/project/create",  responseType: "text", body: new FormData(e.target)}).then(function(result) {
						m.route.set("/projects");
					}).catch(function(error) {
						m.route.set("/projects");
					});
				}}, [
					m("div.field", [
						m("label.label", "Name"),
						m("div.control", m("input", {name: "name"}))
					]),
					m("div.field", [
						m("label.label", "URL"),
						m("div.control", m("input", {name: "url"}))
					]),
					m("div.field", [
						m("label.label", "Branch"),
						m("div.control", m("input", {name: "branch"}))
					]),
					m("div.field", [
						m("label.label", "Destination"),
						m("div.control", m("input", {name: "destination"}))
					]),
					m("div.field", [
						m("label.label", "Tag"),
						m("div.control", m("input", {name: "tag"}))
					]),
					m("div.field", [
						m("div.control", m("button.is-link", "Create"))
					])
				])
			])
		]);
	}
}

let ProjectUpload = {
	view: function(vnode) {
		return m("div.columns", [
			m("div.column", [m(Sidebar)]),
			m("div.column", [
				m("form", {onsubmit: function(e) {
					e.preventDefault()
					console.log(e)
					m.request({method: "POST", url: "/project/upload",  responseType: "text", body: new FormData(e.target)}).then(function(result) {
						m.route.set("/projects");
					}).catch(function(error) {
						m.route.set("/projects");
					});
					
				}}, [
					m("input", {type: "hidden", name: "id", value: vnode.attrs.id}),
					m("div.field", [
						m("div.file.has-name", [
							m("label.file-label", [
								m("input.file-input", {type: "file", name: "file"}),
								m("span.file-cta", [
									m("span.file-icon", m("i.fas.fa-upload")),
									m("span.file-label", "Choose a file…")
								]),
								m("span.file-name")
							])
						])
					]),
					m("div.field", [
						m("label.label", "Name"),
						m("div.control", m("input", {name: "name"}))
					]),
					m("div.field", [
						m("div.control", m("button.is-link", "Upload"))
					])
				])
			])
		]);
	}
}

var logs = "";
var offset = 0;

function taskLogs(vnode) {
	console.log(vnode);
	return m.request({
		url: "/task/logs",
		params: {id: vnode.attrs.id, offset: offset},
		responseType: "text",
		deserialize: function(value) {return value;}
	}).then(function(result) {
		console.log(result);
		logs += result;
		offset = logs.length;
	});
}

let Task = {
	oninit: function(vnode) {
		logs = "";
		offset = 0;
		taskLogs(vnode);
	},
	oncreate: function(vnode) {
		interval = setInterval(taskLogs, 2000, vnode);
	},
	onremove: function(vnode) {
		clearInterval(interval);
	},
	view: function(vnode) {
		return m("pre", logs || "")
	}
}

m.route(document.body, "/projects", {
	"/projects": Projects,
	"/project/create": ProjectCreate,
	"/project/upload/:id": ProjectUpload,
	"/task/:id": Task
})
