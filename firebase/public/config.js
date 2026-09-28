// URL pública do backend Go (deploy no Render).
//
// É pra cá que o painel manda as chamadas da API quando está hospedado no
// Firebase. Se um dia eu trocar o backend de lugar, é só mudar essa linha.
//
// Pra testar com o backend rodando na minha máquina, dá pra apontar pra:
//   window.API_BASE = "http://localhost:8080";
window.API_BASE = "https://gopher-links.onrender.com";
