// Package webui serve o painel visual (HTML/CSS/JS) que fica em cima da
// API. Uso go:embed pra empacotar os arquivos estáticos dentro do próprio
// binário — assim não preciso me preocupar em copiar uma pasta junto quando
// for rodar em outro lugar, é um único executável.
//
// Os mesmos arquivos aqui também são a base da cópia que sobe pro Firebase
// Hosting (ver pasta firebase/). Por isso os caminhos são todos relativos e
// ficam na raiz — assim o mesmo index.html funciona sendo servido pelo Go
// ou pelo Firebase, sem alteração.
package webui

import (
	"embed"
	"net/http"
	"path"
)

//go:embed ui/index.html ui/style.css ui/app.js ui/config.js
var files embed.FS

// contentTypes mapeia a extensão do arquivo pro cabeçalho Content-Type
// correto. Faço na mão porque a detecção automática do Go às vezes erra o
// tipo de CSS/JS, o que faz o navegador recusar o arquivo.
var contentTypes = map[string]string{
	".html": "text/html; charset=utf-8",
	".css":  "text/css; charset=utf-8",
	".js":   "text/javascript; charset=utf-8",
}

// Index serve a página principal do painel.
func Index(w http.ResponseWriter, r *http.Request) {
	serve(w, "ui/index.html")
}

// Asset serve um arquivo estático (style.css, app.js, config.js) pelo nome
// que veio na URL. Registro cada um explicitamente no roteador, então aqui
// só preciso pegar o último trecho do caminho.
func Asset(w http.ResponseWriter, r *http.Request) {
	serve(w, "ui/"+path.Base(r.URL.Path))
}

// serve lê o arquivo embutido e escreve na resposta com o Content-Type
// certo. Se o arquivo não existir (não deveria, já que é tudo embutido em
// tempo de compilação), devolve 404.
func serve(w http.ResponseWriter, name string) {
	data, err := files.ReadFile(name)
	if err != nil {
		http.Error(w, "arquivo não encontrado", http.StatusNotFound)
		return
	}
	if ct, ok := contentTypes[path.Ext(name)]; ok {
		w.Header().Set("Content-Type", ct)
	}
	_, _ = w.Write(data)
}
