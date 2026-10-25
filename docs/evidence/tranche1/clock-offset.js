// This machine's clock runs ahead of real time. CLOCK_OFFSET_MS (negative)
// shifts Date so the suite's timestamps match the servers it checks.
const offset = Number(process.env.CLOCK_OFFSET_MS || 0);
if (offset) {
  const RealDate = Date;
  class ShiftedDate extends RealDate {
    constructor(...args) {
      if (args.length === 0) super(RealDate.now() + offset);
      else super(...args);
    }
    static now() {
      return RealDate.now() + offset;
    }
  }
  global.Date = ShiftedDate;
}
