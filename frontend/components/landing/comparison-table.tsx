import { Check, X, Minus } from 'lucide-react';

export function ComparisonTable() {
  return (
    <section className="w-full max-w-4xl mx-auto px-6 py-16">
      <h2 className="text-3xl font-bold text-center mb-2">Why cloudemu?</h2>
      <p className="text-fd-muted-foreground text-center mb-10">
        Compare approaches to testing cloud-dependent code
      </p>
      <div className="overflow-x-auto rounded-xl border border-fd-border">
        <table className="w-full text-left">
          <thead>
            <tr className="border-b border-fd-border bg-fd-secondary/50">
              <th className="px-6 py-4 font-medium text-fd-muted-foreground">Feature</th>
              <th className="px-6 py-4 font-medium text-fd-muted-foreground">Real Cloud</th>
              <th className="px-6 py-4 font-medium text-fd-muted-foreground">LocalStack / Emulators</th>
              <th className="px-6 py-4 font-medium text-fd-primary border-x border-fd-border bg-fd-primary/5">
                cloudemu
              </th>
            </tr>
          </thead>
          <tbody className="divide-y divide-fd-border">
            <Row
              feature="Cost"
              real="$$$"
              emulator="$"
              cloudemu="Free"
              highlight
            />
            <Row
              feature="Speed"
              real="Seconds"
              emulator="100ms+"
              cloudemu="~10ms"
              highlight
            />
            <Row
              feature="Works Offline"
              real={false}
              emulator={true}
              cloudemu={true}
            />
            <Row
              feature="Docker Required"
              real={false}
              emulator={true}
              cloudemu={false}
              invertBool
            />
            <Row
              feature="Setup"
              real="Account + Credentials"
              emulator="Docker + Config"
              cloudemu="go get"
              highlight
            />
            <Row
              feature="Realistic Behavior"
              real={true}
              emulator="Partial"
              cloudemu={true}
            />
            <Row
              feature="Multi-Cloud"
              real={false}
              emulator={false}
              cloudemu={true}
            />
          </tbody>
        </table>
      </div>
    </section>
  );
}

function Row({
  feature,
  real,
  emulator,
  cloudemu,
  highlight,
  invertBool,
}: {
  feature: string;
  real: string | boolean;
  emulator: string | boolean;
  cloudemu: string | boolean;
  highlight?: boolean;
  invertBool?: boolean;
}) {
  const renderCell = (value: string | boolean, isCloudemu = false) => {
    if (typeof value === 'boolean') {
      const good = invertBool ? !value : value;
      return good ? (
        <Check className="w-5 h-5 text-green-500" />
      ) : (
        <X className="w-5 h-5 text-red-400" />
      );
    }
    return (
      <span className={isCloudemu && highlight ? 'font-semibold text-fd-primary' : ''}>
        {value}
      </span>
    );
  };

  return (
    <tr>
      <td className="px-6 py-4 font-medium">{feature}</td>
      <td className="px-6 py-4">{renderCell(real)}</td>
      <td className="px-6 py-4">{renderCell(emulator)}</td>
      <td className="px-6 py-4 border-x border-fd-border bg-fd-primary/5">
        {renderCell(cloudemu, true)}
      </td>
    </tr>
  );
}
