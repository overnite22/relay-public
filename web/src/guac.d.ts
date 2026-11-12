declare module "guacamole-common-js" {
  // La bibliothèque n'a pas de types officiels à jour pour la 1.5 : on la
  // manipule via quelques interfaces minimales dans SessionView.tsx.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const Guacamole: any;
  export default Guacamole;
}
