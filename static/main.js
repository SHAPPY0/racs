Element.prototype.remove = function() {
	if (this.parentNode) this.parentNode.removeChild(this);
};

Element.prototype.removeAfterTransition = function() {
	var element = this;
	function remove() {
		element.removeEventListener("webkitTransitionEnd", remove);
		element.removeEventListener("transitionend", remove);
		element.remove();
	}
	element.addEventListener("webkitTransitionEnd", remove);
	element.addEventListener("transitionend", remove);
};

Element.prototype.replace = function(other) {
	if (this.parentNode) this.parentNode.replaceChild(other, this);
};

Element.prototype.removeChildren = function() {
	var child;
	while (child = this.firstChild) this.removeChild(child);
};

Element.prototype.prependChild = function(child) {
	return this.insertBefore(child, this.firstChild);
};

Element.prototype.insertAfter = function(child, after) {
	return this.insertBefore(child, after.nextSibling);
};

Element.prototype.addClass = function(_class) {
	this.classList.add(_class);
};

Element.prototype.removeClass = function(_class) {
	this.classList.remove(_class);
};

Element.prototype.toggleClass = function(_class) {
	this.classList.toggle(_class);
};

function m(tag, attrs, children, events) {
	let classes = tag.split(".");
	let element = document.createElement(classes[0]);
	for (var i = 1; i < classes.length; ++i) element.addClass(classes[i]);
	if (attrs) for (var attr in attrs) element.setAttribute(attr, attrs[attr]);
	if (children) for (var i = 0; i < children.length; ++i) {
		var child = children[i];
		if (!child) {
			element.appendChild(document.createTextNode(""));
		} else if (typeof child === "function") {
			var textNode = document.createTextNode(child());
			textNode.textFunc = child;
			element.appendChild(textNode);
		} else if (typeof child === "string") {
			element.appendChild(document.createTextNode(child));
		} else {
			element.appendChild(child);
		}
	}
	if (events) for (var event in events) element.addEventListener(event, events[event]);
	return element;
}


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

function projectList() {
	fetch("/project/list").then(response => response.json()).then(result => {
		projects = result;
	});
}

function projectBuild(project, event) {
	let stage = event.target.value;
	event.target.value = null;
	fetch("/project/build", {
		method: "POST",
		headers: {"Content-Type", "application/json"},
		body: {id: project.id, stage: stage}
	});
}

var interval = setInterval(projectsList, 2000);

function projectView() {
	document.body.removeChildren();
	document.body.appendChild(m("div.columns", [
		m("div.column.is-narrow", [m(Sidebar)]),
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
									m("td", task.state),
									m("td", task.time)
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
	]));
}

let ProjectCreate = {
	view: function(vnode) {
		return m("div.columns", [
			m("div.column.is-narrow", [m(Sidebar)]),
			m("div.column", [
				m("form", {onsubmit: function(e) {
					e.preventDefault()
					console.log(e)
					m.request({method: "POST", url: "/project/create",  responseType: "text", body: new FormData(e.target)}).then(function(result) {
						m.route.set("/projects");
					});
				}}, [
					m("div.field", [
						m("label.label", "Name"),
						m("div.control", m("input.input", {name: "name"}))
					]),
					m("div.field", [
						m("label.label", "URL"),
						m("div.control", m("input.input", {name: "url"}))
					]),
					m("div.field", [
						m("label.label", "Branch"),
						m("div.control", m("input.input", {name: "branch"}))
					]),
					m("div.field", [
						m("label.label", "Destination"),
						m("div.control", m("input.input", {name: "destination"}))
					]),
					m("div.field", [
						m("label.label", "Tag"),
						m("div.control", m("input.input", {name: "tag"}))
					]),
					m("div.field", [
						m("div.control", m("button.button.is-link", "Create"))
					])
				])
			])
		]);
	}
}

let ProjectUpload = {
	view: function(vnode) {
		let filename = m("span.file-name");
		let uploadname = m("input.input", {name: "name"});
		return m("div.columns", [
			m("div.column.is-narrow", [m(Sidebar)]),
			m("div.column", [
				m("form", {onsubmit: function(e) {
					e.preventDefault()
					console.log(e)
					m.request({method: "POST", url: "/project/upload",  responseType: "text", body: new FormData(e.target)}).then(function(result) {
						m.route.set("/projects");
					});
					
				}}, [
					m("input", {type: "hidden", name: "id", value: vnode.attrs.id}),
					m("div.field", [
						m("div.file.has-name", [
							m("label.file-label", [
								m("input.file-input", {type: "file", name: "file", onchange: function(e) {
									filename.dom.textContent = e.target.files[0].name;
									uploadname.dom.value = e.target.files[0].name;
								}}),
								m("span.file-cta", [
									m("span.file-icon", m("i.fas.fa-upload")),
									m("span.file-label", "Choose a file…")
								]),
								filename
							])
						])
					]),
					m("div.field", [
						m("label.label", "Name"),
						m("div.control", uploadname)
					]),
					m("div.field", [
						m("div.control", m("button.button.is-link", "Upload"))
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
