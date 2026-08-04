package id

// One Kind per entity family that appears on the wire. Adding an entity means
// adding one struct here.
//
// A prefix is permanent: it is mixed into the cipher tweak, so changing it
// changes every encoded id of that kind. Keep them three lowercase letters, and
// never reuse one for a different entity.
type (
	Account          struct{} // account.account
	Contact          struct{} // account.contact
	Device           struct{} // account.device
	IdentityDocument struct{} // account.identity_document
	Category         struct{} // catalog.category
	Listing          struct{} // catalog.listing
	Variant          struct{} // catalog.variant
	CartItem         struct{} // order.cart_item
	DraftOrder       struct{} // order.draft_order
	Transport        struct{} // order.transport
	Order            struct{} // order.order
	Item             struct{} // order.item
	Refund           struct{} // order.refund
	Offer            struct{} // order.offer
	PaymentSession   struct{} // finance.payment_session
	Transaction      struct{} // finance.transaction
	BankAccount      struct{} // finance.bank_account
	Feedback         struct{} // trust.feedback
	Review           struct{} // trust.review
	ReviewReply      struct{} // trust.review_reply
	Ticket           struct{} // trust.ticket
	Conversation     struct{} // chat.conversation
	Message          struct{} // chat.message
	Resource         struct{} // common.resource
)

func (Account) Prefix() string          { return "acc" }
func (Contact) Prefix() string          { return "ctc" }
func (Device) Prefix() string           { return "dvc" }
func (IdentityDocument) Prefix() string { return "idd" }
func (Category) Prefix() string         { return "cat" }
func (Listing) Prefix() string          { return "lst" }
func (Variant) Prefix() string          { return "vrn" }
func (CartItem) Prefix() string         { return "crt" }
func (DraftOrder) Prefix() string       { return "drf" }
func (Transport) Prefix() string        { return "trp" }
func (Order) Prefix() string            { return "ord" }
func (Item) Prefix() string             { return "itm" }
func (Refund) Prefix() string           { return "rfd" }
func (Offer) Prefix() string            { return "ofr" }
func (PaymentSession) Prefix() string   { return "pay" }
func (Transaction) Prefix() string      { return "txn" }
func (BankAccount) Prefix() string      { return "bnk" }
func (Feedback) Prefix() string         { return "fbk" }
func (Review) Prefix() string           { return "rvw" }
func (ReviewReply) Prefix() string      { return "rpl" }
func (Ticket) Prefix() string           { return "tkt" }
func (Conversation) Prefix() string     { return "cnv" }
func (Message) Prefix() string          { return "msg" }
func (Resource) Prefix() string         { return "res" }
