/* ---------- External ---------- */
import Newstack from "@moureau/newstack";

/**
 * @description
 * Inline, copyable install command for the hero.
 */
export class CopyCommand extends Newstack {
  /* ---------- State ---------- */
  command = "curl -fsSL https://menina.moureau.dev/install.sh | sudo sh";
  copied = false;

  /* ---------- Actions ---------- */
  async copy() {
    try {
      if (typeof navigator !== "undefined" && navigator.clipboard) {
        await navigator.clipboard.writeText(this.command);
      }
      this.copied = true;
    } catch {
      this.copied = false;
    }
  }

  /* ---------- Render ---------- */
  render() {
    return (
      <div class="cmd">
        <span class="cmd-prompt">$</span>
        <code class="cmd-code">{this.command}</code>
        <button class="cmd-copy" type="button" onclick={() => this.copy()}>
          {this.copied ? "Copied" : "Copy"}
        </button>
      </div>
    );
  }
}
