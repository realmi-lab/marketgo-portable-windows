import { AlertTriangle, RotateCcw } from "lucide-react";
import { Component, ReactNode } from "react";

import { isStaleBuildError, reloadForNewBuild } from "@/lib/stale-build-reload";

interface Props {
  children: ReactNode;
}

interface State {
  hasError: boolean;
  staleBuild: boolean;
}

class ErrorBoundary extends Component<Props, State> {
  constructor(props: Props) {
    super(props);
    this.state = { hasError: false, staleBuild: false };
  }

  static getDerivedStateFromError(error: Error): State {
    return { hasError: true, staleBuild: isStaleBuildError(error) };
  }

  private retryAfterReconnect = () => {
    if (this.state.staleBuild) reloadForNewBuild();
  };

  componentDidMount() {
    window.addEventListener("online", this.retryAfterReconnect);
  }

  componentWillUnmount() {
    window.removeEventListener("online", this.retryAfterReconnect);
  }

  componentDidCatch(error: Error) {
    // The technical detail stays in the console, not on the user's screen.
    console.error(error);
    // Only a failure needed to render the screen can trigger recovery.
    // Background prefetch failures must leave the current screen and drafts up.
    if (isStaleBuildError(error)) reloadForNewBuild();
  }

  render() {
    if (this.state.hasError) {
      return (
        <div className="flex min-h-screen items-center justify-center bg-background p-8">
          <div className="flex w-full max-w-2xl flex-col items-center p-8 text-center">
            <AlertTriangle
              size={48}
              className="mb-6 flex-shrink-0 text-destructive"
            />
            <h2 className="mb-2">화면을 불러오지 못했습니다.</h2>
            <p className="mb-6 text-muted-foreground">
              {this.state.staleBuild
                ? "화면 파일을 불러오지 못했습니다. 연결이 돌아오면 다시 시도하며, 필요하면 새로고침해 주세요."
                : "잠시 후 새로고침해 주세요."}
            </p>
            <button
              onClick={() => window.location.reload()}
              className="flex cursor-pointer items-center gap-2 rounded-lg bg-primary px-4 py-2 text-primary-foreground hover:opacity-90"
            >
              <RotateCcw size={16} />
              새로고침
            </button>
          </div>
        </div>
      );
    }

    return this.props.children;
  }
}

export default ErrorBoundary;
