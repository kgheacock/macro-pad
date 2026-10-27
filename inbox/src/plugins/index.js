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
//   feed           optional; what the daemon runs to write the plugin's own
//                  messages (see daemon.js)
//   createRenderer optional; draws the plugin's own keys, more than one top
//                  key (see daemon.js; apps, task 0085)
//   press          optional; acts on a press on a child of those keys
//
// To add one, write plugins/NAME.js and list it here.

import { UsageError } from '../errors.js';
import { DEFAULT_PLUGIN } from '../store.js';
import ai from './ai.js';
import apps from './apps.js';
import basic from './basic.js';
import github from './github.js';

export { DEFAULT_PLUGIN };
export const PLUGINS = { basic, ai, github, apps };

export function getPlugin(name = DEFAULT_PLUGIN) {
  if (Object.hasOwn(PLUGINS, name)) return PLUGINS[name];
  throw new UsageError(`unknown plugin "${name}"; use one of: ${Object.keys(PLUGINS).join(', ')}`, {
    help: false,
  });
}
