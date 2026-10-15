import Link from "next/link";

export default function Home() {
  return (
    <>
      <h1>Geminus</h1>
      <p>
        <Link href="/projects">Go to projects</Link>
      </p>
    </>
  );
}
