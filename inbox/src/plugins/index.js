// The plugins of the inbox. A plugin holds only what one kind of inbox does
// differently; the store, the CLI, "here", and the daemon are shared. Each
// message in the store belongs to one plugin, and every command reads and
// writes only the messages of the plugin it runs with (--plugin NAME).
//
// A plugin is
//   name           the word for --plugin, and the value of messages.plugin
//   summary        one line for `inbox plugins` and the help
//   statuses       the statuses --status accepts, or null for any word of
//                  letters, digits, - and _
//   defaultStatus  the status of a message sent without --status
//   about          a paragraph added to the help, or null
//   rules          what its key and the children show (see render.js)
//
// To add one, write plugins/NAME.js and list it here.

import { UsageError } from '../errors.js';
import { DEFAULT_PLUGIN } from '../store.js';
import ai from './ai.js';
import basic from './basic.js';

export { DEFAULT_PLUGIN };
export const PLUGINS = { basic, ai };

export function getPlugin(name = DEFAULT_PLUGIN) {
  if (Object.hasOwn(PLUGINS, name)) return PLUGINS[name];
  throw new UsageError(`unknown plugin "${name}"; use one of: ${Object.keys(PLUGINS).join(', ')}`, {
    help: false,
  });
}
