import React from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import App from "./App";
import "./style.css";
import "./image2.css";

const queryClient = new QueryClient();

window.addEventListener("keydown", (event) => {
  if (!(event.metaKey || event.ctrlKey) || event.key.toLowerCase() !== "k") return;

  const searchInput = document.querySelector<HTMLInputElement>(".search-box input");
  if (!searchInput) return;

  event.preventDefault();
  searchInput.focus();
  searchInput.select();
});

createRoot(document.getElementById("root")!).render(<React.StrictMode><QueryClientProvider client={queryClient}><App /></QueryClientProvider></React.StrictMode>);
