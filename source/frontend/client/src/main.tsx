import { createRoot } from "react-dom/client";
import "pretendard/dist/web/variable/pretendardvariable-dynamic-subset.css";
import App from "./App";
import "./index.css";
import "./dashboard-shell.css";
import "./help.css";
import "./notices.css";
import "./dashboard-polish.css";
import "./mobile.css";
import "./screens/screens.css";

const root = createRoot(document.getElementById("root")!);
root.render(<App />);
