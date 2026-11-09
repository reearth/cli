import { useCallback } from "react";

import { postMsg } from "@/shared/utils";

export default () => {
  const handleHello = useCallback(() => {
    postMsg("hello", { at: new Date().toISOString() });
  }, []);

  return { handleHello };
};
