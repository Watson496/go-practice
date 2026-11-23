import { Button, Group, Modal, Select, Stack, Text } from "@mantine/core";
import { useState } from "react";
import type { Color, GameResponse } from "./api";
import {
  aiLevelDescriptions,
  aiLevels,
  type ConfiguredSettings,
  humanColors,
} from "./gameSettings";

type GameSettingsModalProps = {
  game: GameResponse | null;
  opened: boolean;
  busy: boolean;
  onClose: () => void;
  onSubmit: (settings: ConfiguredSettings) => void;
  onInvalid: (message: string) => void;
};

export function GameSettingsModal(props: GameSettingsModalProps) {
  return (
    <Modal opened={props.opened} onClose={props.onClose} title="対局の設定">
      {props.opened && props.game ? (
        <GameSettingsForm {...props} game={props.game} />
      ) : null}
    </Modal>
  );
}

function GameSettingsForm(
  props: GameSettingsModalProps & { game: GameResponse },
) {
  const [humanColor, setHumanColor] = useState<string | null>(
    props.game.humanColor,
  );
  const [aiLevel, setAILevel] = useState<string | null>(
    props.game.aiLevel === null ? null : String(props.game.aiLevel),
  );
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        if (humanColor === null || aiLevel === null) {
          props.onInvalid("あなたの石の色とAIレベルを選んでください。");
          return;
        }
        props.onSubmit({
          aiLevel: Number(aiLevel),
          humanColor: humanColor as Color,
        });
      }}
    >
      <Stack gap="sm">
        <Group grow>
          <Select
            label="あなた"
            placeholder="未設定"
            data={humanColors}
            value={humanColor}
            onChange={setHumanColor}
            allowDeselect={false}
            comboboxProps={{ withinPortal: false }}
          />
          <Select
            label="AIレベル"
            placeholder="未設定"
            data={aiLevels}
            value={aiLevel}
            onChange={setAILevel}
            allowDeselect={false}
            comboboxProps={{ withinPortal: false }}
          />
        </Group>
        <Text size="xs" c="dimmed">
          盤サイズ{props.game.game.boardSize}路・
          {props.game.game.handicap > 0
            ? `${props.game.game.handicap}子局`
            : "互先"}
          ・コミ {props.game.game.komi}目は変更できません。
        </Text>
        {aiLevel !== null ? (
          <Text size="xs" c="dimmed">
            {aiLevelDescriptions[aiLevel]}
          </Text>
        ) : null}
        <Group justify="flex-end" gap="xs">
          <Button
            variant="default"
            onClick={props.onClose}
            disabled={props.busy}
          >
            キャンセル
          </Button>
          <Button type="submit" loading={props.busy}>
            変更する
          </Button>
        </Group>
      </Stack>
    </form>
  );
}
