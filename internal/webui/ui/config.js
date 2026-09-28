// Endereço do backend Go que o painel vai chamar.
//
// Deixo vazio por padrão, o que faz o painel chamar a própria origem em que
// ele está sendo servido — perfeito quando o próprio Go serve tudo junto
// (rodando localmente, por exemplo).
//
// Quando eu subo o frontend separado no Firebase Hosting, a cópia que vai
// pra lá troca esse valor pela URL pública do backend, tipo:
//   window.API_BASE = "https://gopher-links.onrender.com";
window.API_BASE = "";
