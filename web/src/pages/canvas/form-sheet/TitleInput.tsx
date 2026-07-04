import { TextInput } from '../../../components/ui';

export function TitleInput({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  return <TextInput value={value} onChange={(event) => onChange(event.target.value)} />;
}
