import { createRoot } from "react-dom/client";
import Pricing from "./Pricing";
import "./style.css";

const root = document.getElementById("app");
if (root) createRoot(root).render(<Pricing />);
