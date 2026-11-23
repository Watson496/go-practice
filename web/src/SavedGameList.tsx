import {
  type Announcements,
  closestCenter,
  DndContext,
  type DragEndEvent,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
} from "@dnd-kit/core";
import {
  arrayMove,
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import {
  ActionIcon,
  Box,
  Button,
  CloseButton,
  Group,
  Modal,
  NavLink,
  ScrollArea,
  Stack,
  Text,
  TextInput,
} from "@mantine/core";
import {
  IconGripVertical,
  IconPencil,
  IconSearch,
  IconTrash,
} from "@tabler/icons-react";
import { type ReactNode, useRef, useState } from "react";

type SavedGameItem = { id: string; name: string };

type SavedGameListProps = {
  games: SavedGameItem[];
  currentId: string | null;
  disabled: boolean;
  onOpen: (id: string) => void;
  // 変更できたときにtrueを返す。変更できなかったときは、Modalを開いたまま入力を残す
  onRename: (id: string, name: string) => Promise<boolean>;
  onDelete: (id: string) => Promise<void>;
  onReorder: (ids: string[]) => void;
  onInteractionStart: (
    kind: "rename" | "reorder" | "delete-game",
    gameId?: string,
  ) => Promise<boolean>;
  onInteractionEnd: (kind: "rename" | "reorder" | "delete-game") => void;
};

const dragInstructions =
  "Spaceキーで持ち上げ、上下の矢印キーで動かし、もう一度Spaceキーで置きます。Escapeキーで取り消します。";

export function SavedGameList(props: SavedGameListProps) {
  const [query, setQuery] = useState("");
  const [renamingId, setRenamingId] = useState<string | null>(null);
  const [renameSaving, setRenameSaving] = useState(false);
  const [deletingId, setDeletingId] = useState<string | null>(null);
  const normalizedQuery = query.trim().toLowerCase();
  const visible = normalizedQuery
    ? props.games.filter((game) =>
        game.name.toLowerCase().includes(normalizedQuery),
      )
    : props.games;
  const renamingGame = props.games.find((game) => game.id === renamingId);
  const deletingGame = props.games.find((game) => game.id === deletingId);
  const rowProps = {
    currentId: props.currentId,
    disabled: props.disabled,
    onOpen: props.onOpen,
    onRenameStart: async (id: string) => {
      if (await props.onInteractionStart("rename", id)) setRenamingId(id);
    },
    onDeleteStart: async (id: string) => {
      if (await props.onInteractionStart("delete-game", id)) setDeletingId(id);
    },
  };

  return (
    <Stack gap="xs" h="100%" className="saved-game-list">
      <TextInput
        aria-label="対局を検索"
        placeholder="対局を検索"
        leftSection={<IconSearch size={16} />}
        value={query}
        onChange={(event) => setQuery(event.currentTarget.value)}
        rightSection={
          query ? (
            <CloseButton
              size="sm"
              aria-label="検索の文字を消す"
              onClick={() => setQuery("")}
            />
          ) : null
        }
      />
      <ScrollArea className="saved-game-scroll" type="auto">
        <ul className="saved-game-items" aria-label="保存した対局">
          {normalizedQuery ? (
            visible.map((game) => (
              <Group component="li" key={game.id} gap={0} wrap="nowrap">
                <Box className="saved-game-handle-space" />
                <SavedGameRow game={game} {...rowProps} />
              </Group>
            ))
          ) : (
            <ReorderContext
              games={props.games}
              onReorder={props.onReorder}
              onInteractionStart={() =>
                props.onInteractionStart("reorder", undefined)
              }
              onInteractionEnd={() => props.onInteractionEnd("reorder")}
            >
              {props.games.map((game) => (
                <SortableSavedGameRow key={game.id} game={game} {...rowProps} />
              ))}
            </ReorderContext>
          )}
        </ul>
        {visible.length === 0 ? (
          <Text size="sm" c="dimmed" p="sm">
            {props.games.length === 0
              ? "保存した対局はありません"
              : "一致する対局はありません"}
          </Text>
        ) : null}
      </ScrollArea>

      <Modal
        opened={Boolean(renamingGame)}
        onClose={() => {
          if (renameSaving) return;
          setRenamingId(null);
          props.onInteractionEnd("rename");
        }}
        closeOnEscape={!renameSaving}
        closeOnClickOutside={!renameSaving}
        withCloseButton={!renameSaving}
        title="対局名を変更"
      >
        {renamingGame ? (
          <RenameForm
            game={renamingGame}
            saving={renameSaving}
            disabled={props.disabled}
            onSave={async (name) => {
              // 応答が届くまでModalを開いたままにし、変更できなかったときは入力を残す
              if (name !== renamingGame.name) {
                setRenameSaving(true);
                const renamed = await props
                  .onRename(renamingGame.id, name)
                  .finally(() => setRenameSaving(false));
                if (!renamed) return;
              }
              setRenamingId(null);
              props.onInteractionEnd("rename");
            }}
            onCancel={() => {
              setRenamingId(null);
              props.onInteractionEnd("rename");
            }}
          />
        ) : null}
      </Modal>
      <Modal
        opened={Boolean(deletingGame)}
        onClose={() => {
          setDeletingId(null);
          props.onInteractionEnd("delete-game");
        }}
        title="対局を削除"
      >
        {deletingGame ? (
          <Stack gap="sm">
            <Text size="sm">
              「{deletingGame.name}」を削除しますか？この操作は取り消せません。
            </Text>
            <Group justify="flex-end" gap="xs">
              <Button
                variant="default"
                onClick={() => {
                  setDeletingId(null);
                  props.onInteractionEnd("delete-game");
                }}
                data-autofocus
              >
                キャンセル
              </Button>
              <Button
                color="red"
                disabled={props.disabled}
                onClick={() => {
                  setDeletingId(null);
                  props.onInteractionEnd("delete-game");
                  void props.onDelete(deletingGame.id);
                }}
              >
                削除
              </Button>
            </Group>
          </Stack>
        ) : null}
      </Modal>
    </Stack>
  );
}

type RowProps = {
  game: SavedGameItem;
  currentId: string | null;
  disabled: boolean;
  onOpen: (id: string) => void;
  onRenameStart: (id: string) => void;
  onDeleteStart: (id: string) => void;
};

function SavedGameRow({
  game,
  currentId,
  disabled,
  onOpen,
  onRenameStart,
  onDeleteStart,
}: RowProps) {
  const current = game.id === currentId;
  return (
    <Group gap={2} wrap="nowrap" className="saved-game-row">
      <NavLink
        component="button"
        type="button"
        label={game.name}
        active={current}
        aria-current={current ? "true" : undefined}
        disabled={disabled || current}
        aria-disabled={disabled || current || undefined}
        onClick={() => {
          if (!disabled && !current) onOpen(game.id);
        }}
        noWrap
        className={`saved-game-link${current && !disabled ? " is-open-game" : ""}`}
      />
      <ActionIcon
        variant="subtle"
        color="gray"
        aria-label={`${game.name}の名前を変更`}
        title="名前を変更"
        disabled={disabled}
        onClick={() => onRenameStart(game.id)}
      >
        <IconPencil size={16} />
      </ActionIcon>
      <ActionIcon
        variant="subtle"
        color="red"
        aria-label={`${game.name}を削除`}
        title="削除"
        disabled={disabled}
        onClick={() => onDeleteStart(game.id)}
      >
        <IconTrash size={16} />
      </ActionIcon>
    </Group>
  );
}

function SortableSavedGameRow(props: RowProps) {
  const {
    attributes,
    listeners,
    setNodeRef,
    setActivatorNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({
    id: props.game.id,
    disabled: props.disabled,
    attributes: { roleDescription: "並べ替えできる項目" },
  });
  // 一覧は拡大縮小した画面の中にあるので、画面上で測った移動量を倍率で割って当てる
  const scale = screenScale();
  return (
    <li
      ref={setNodeRef}
      className="saved-game-sortable"
      data-dragging={isDragging || undefined}
      style={{
        transform: transform
          ? `translate3d(${transform.x / scale}px, ${transform.y / scale}px, 0)`
          : undefined,
        transition,
      }}
    >
      <Group gap={0} wrap="nowrap">
        <Box
          ref={setActivatorNodeRef}
          {...attributes}
          {...listeners}
          aria-label={`${props.game.name}を並べ替える`}
          className="saved-game-handle"
        >
          <IconGripVertical size={16} />
        </Box>
        <SavedGameRow {...props} />
      </Group>
    </li>
  );
}

function screenScale() {
  const screen = document.querySelector<HTMLElement>(".app-screen");
  if (!screen || screen.offsetWidth === 0) return 1;
  return screen.getBoundingClientRect().width / screen.offsetWidth;
}

function ReorderContext({
  games,
  onReorder,
  onInteractionStart,
  onInteractionEnd,
  children,
}: {
  games: SavedGameItem[];
  onReorder: (ids: string[]) => void;
  onInteractionStart: () => Promise<boolean>;
  onInteractionEnd: () => void;
  children: ReactNode;
}) {
  const interactionStart = useRef<Promise<boolean> | null>(null);
  const sensors = useSensors(
    useSensor(PointerSensor),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    }),
  );
  const ids = games.map((game) => game.id);
  const nameOf = (id: string | number) =>
    games.find((game) => game.id === id)?.name ?? "";
  const position = (id: string | number) => ids.indexOf(String(id)) + 1;
  const announcements: Announcements = {
    onDragStart: ({ active }) =>
      `${nameOf(active.id)}を持ち上げました。${position(active.id)}番目です。`,
    onDragOver: ({ over }) =>
      over ? `${position(over.id)}番目に移動しました。` : undefined,
    onDragEnd: ({ active, over }) =>
      over
        ? `${nameOf(active.id)}を${position(over.id)}番目に置きました。`
        : "並べ替えを取り消しました。",
    onDragCancel: () => "並べ替えを取り消しました。",
  };
  const onDragEnd = async ({ active, over }: DragEndEvent) => {
    const started = await interactionStart.current;
    interactionStart.current = null;
    if (started && over && active.id !== over.id)
      onReorder(
        arrayMove(
          ids,
          ids.indexOf(String(active.id)),
          ids.indexOf(String(over.id)),
        ),
      );
    onInteractionEnd();
  };
  return (
    <DndContext
      sensors={sensors}
      collisionDetection={closestCenter}
      accessibility={{
        announcements,
        screenReaderInstructions: { draggable: dragInstructions },
      }}
      onDragStart={() => {
        interactionStart.current = onInteractionStart();
      }}
      onDragEnd={(event) => void onDragEnd(event)}
      onDragCancel={() => {
        void interactionStart.current?.finally(() => {
          interactionStart.current = null;
          onInteractionEnd();
        });
      }}
    >
      <SortableContext items={ids} strategy={verticalListSortingStrategy}>
        {children}
      </SortableContext>
    </DndContext>
  );
}

function RenameForm({
  game,
  saving,
  disabled,
  onSave,
  onCancel,
}: {
  game: SavedGameItem;
  saving: boolean;
  disabled: boolean;
  onSave: (name: string) => Promise<void>;
  onCancel: () => void;
}) {
  const [value, setValue] = useState(game.name);
  return (
    <form
      onSubmit={(event) => {
        event.preventDefault();
        void onSave(value);
      }}
    >
      <Stack gap="sm">
        <TextInput
          label="対局名"
          data-autofocus
          value={value}
          onChange={(event) => setValue(event.currentTarget.value)}
        />
        <Group justify="flex-end" gap="xs">
          <Button variant="default" onClick={onCancel} disabled={saving}>
            キャンセル
          </Button>
          <Button type="submit" loading={saving} disabled={disabled}>
            変更
          </Button>
        </Group>
      </Stack>
    </form>
  );
}
