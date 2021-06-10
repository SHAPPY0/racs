let Sidebar = {
	view: function() {
		return m("aside.menu", [])
	}
}

var projects = [];

function projectsList() {
	return m.request({url: "/project/list"}).then(function(result) {
		projects = result;
	});
}

let Projects = {
	oninit: projectsList,
	view: function() {
		return m("div.columns", [
			m("div.column", [m(Sidebar)]),
			m("div.column", [
				m("table.table", [
					m("thead", [
						m("tr", [
							m("th", ["Id"]),
							m("th", ["Name"]),
							m("th", ["State"]),
							m("th", ["Task"]),
							m("th", ["Version"])
						])
					]),
					m("tbody", projects.map(function(project) {
						return m("tr", [
							m("td", [project.id.toString()]),
							m("td", [project.name.toString()]),
							m("td", [project.state]),
							m("td", [(project.task || "").toString()]),
							m("td", [project.version.toString()])
						]);				
					}))
				])
			])
		])
	}
}

setInterval(projectsList, 2000)

m.route(document.body, "/projects", {
	"/projects": Projects
})
