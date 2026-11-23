import { Alert, Button, Container, Stack } from "@mantine/core";
import { IconAlertCircle, IconRefresh } from "@tabler/icons-react";
import { Component, type ErrorInfo, type ReactNode } from "react";

type Props = { children: ReactNode };
type State = { error: Error | null };

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error("Uncaught application error", error, info);
  }

  render() {
    if (!this.state.error) return this.props.children;

    return (
      <Container size="sm" py="xl">
        <Stack>
          <Alert
            color="red"
            icon={<IconAlertCircle size={18} />}
            title="画面を表示できませんでした"
          >
            {this.state.error.message}
          </Alert>
          <Button
            leftSection={<IconRefresh size={18} />}
            onClick={() => window.location.reload()}
          >
            再読み込み
          </Button>
        </Stack>
      </Container>
    );
  }
}
