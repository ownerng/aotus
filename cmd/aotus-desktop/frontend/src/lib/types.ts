// What the chat keeps in memory for each line on screen.
export type ChatItem = {
  key: string;
  turnId: string;
  kind: "user" | "assistant" | "tool" | "error" | "note";
  text: string;
  pending?: boolean; // the answer is still streaming
  more?: boolean;    // an old turn whose answer has not been loaded yet
};
