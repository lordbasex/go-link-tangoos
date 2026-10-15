// Aplica el tema claro guardado antes de pintar (archivo externo: la CSP no permite scripts inline).
// Oscuro es el predeterminado, como en desk.go-link.org.
try {
  if (window.localStorage.getItem("tangoos-connect.theme") === "light") {
    document.documentElement.dataset.theme = "light";
    document.querySelector('meta[name="theme-color"]').setAttribute("content", "#f3f4f7");
  }
} catch (e) {
  // almacenamiento bloqueado: oscuro
}
